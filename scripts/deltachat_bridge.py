#!/usr/bin/env python3

from __future__ import annotations

import asyncio
import json
import logging
import os
import signal
import sqlite3
import threading
import time
from dataclasses import dataclass
from pathlib import Path
from queue import Queue
from typing import Any

import websockets
from deltachat_rpc_client import DeltaChat, EventType, Rpc, SpecialContactId
from deltachat_rpc_client.const import ViewType
from websockets.exceptions import ConnectionClosed

_IMAGE_EXTENSIONS = frozenset((".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".bmp", ".tiff"))

ACCOUNTS_TOML_TEMPLATE = """accounts = []
selected_account = 0
next_id = 1
accounts_order = []
"""


def env_int(name: str, default: int) -> int:
    raw = os.getenv(name)
    if raw is None or raw == "":
        return default
    return int(raw)


@dataclass(frozen=True)
class Settings:
    host: str
    port: int
    accounts_dir: str
    rpc_server_path: str
    setup_qr: str
    contact_qr_text_file: str
    contact_qr_svg_file: str
    email: str
    password: str
    display_name: str
    configure_timeout_seconds: int
    ready_file: str
    log_level: str

    @staticmethod
    def from_env() -> "Settings":
        return Settings(
            host=os.getenv("DELTACHAT_BRIDGE_HOST", "0.0.0.0"),
            port=env_int("DELTACHAT_BRIDGE_PORT", 3100),
            accounts_dir=os.getenv("DELTACHAT_ACCOUNTS_DIR", "/accounts"),
            rpc_server_path=os.getenv("DELTACHAT_RPC_SERVER_PATH", "deltachat-rpc-server"),
            setup_qr=os.getenv("DELTACHAT_SETUP_QR", "DCACCOUNT:https://nine.testrun.org/new"),
            contact_qr_text_file=os.getenv("DELTACHAT_CONTACT_QR_TEXT_FILE", "/root/.picoclaw/deltachat-contact.qr.txt"),
            contact_qr_svg_file=os.getenv("DELTACHAT_CONTACT_QR_SVG_FILE", "/root/.picoclaw/deltachat-contact.qr.svg"),
            email=os.getenv("DELTACHAT_EMAIL", "").strip(),
            password=os.getenv("DELTACHAT_PASSWORD", ""),
            display_name=os.getenv("DELTACHAT_DISPLAY_NAME", "PicoClaw Bridge"),
            configure_timeout_seconds=env_int("DELTACHAT_CONFIGURE_TIMEOUT_SECONDS", 300),
            ready_file=os.getenv("DELTACHAT_BRIDGE_READY_FILE", "/tmp/deltachat-bridge.ready"),
            log_level=os.getenv("DELTACHAT_BRIDGE_LOG_LEVEL", "INFO").upper(),
        )


class DeltaChatBridge:
    def __init__(self, settings: Settings):
        self.settings = settings
        self.stop_event = threading.Event()
        self.delta_to_ws_queue: Queue[dict[str, Any] | None] = Queue()
        self.clients: set[Any] = set()
        self.clients_lock = threading.Lock()

        self.rpc: Rpc | None = None
        self.delta_chat: DeltaChat | None = None
        self.account: Any | None = None
        self.reader_thread: threading.Thread | None = None

    def _set_ready_flag(self, ready: bool) -> None:
        if self.settings.ready_file == "":
            return

        path = Path(self.settings.ready_file)
        if ready:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("ready\n", encoding="utf-8")
            return

        try:
            path.unlink(missing_ok=True)
        except Exception:
            pass

    @staticmethod
    def _write_file(path_value: str, data: str) -> None:
        if path_value == "":
            return
        path = Path(path_value)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(data, encoding="utf-8")

    def _export_contact_qr(self, account: Any) -> None:
        qr_text = account.get_qr_code()
        self._write_file(self.settings.contact_qr_text_file, qr_text + "\n")
        logging.info("Delta Chat contact QR text saved to %s", self.settings.contact_qr_text_file)

        if self.settings.contact_qr_svg_file == "":
            return

        try:
            _, qr_svg = account.get_qr_code_svg()
            self._write_file(self.settings.contact_qr_svg_file, qr_svg)
            logging.info("Delta Chat contact QR SVG saved to %s", self.settings.contact_qr_svg_file)
        except Exception as exc:
            logging.warning("Failed to export Delta Chat contact QR SVG: %s", exc)

    def stop(self) -> None:
        if self.stop_event.is_set():
            return
        self.stop_event.set()
        self.delta_to_ws_queue.put(None)
        if self.rpc is not None:
            try:
                self.rpc.close()
            except Exception:
                pass

    def _ensure_accounts_store(self) -> None:
        accounts_root = Path(self.settings.accounts_dir)
        accounts_root.mkdir(parents=True, exist_ok=True)
        accounts_file = accounts_root / "accounts.toml"
        if not accounts_file.exists():
            accounts_file.write_text(ACCOUNTS_TOML_TEMPLATE, encoding="utf-8")

    def _configure_account(self) -> None:
        assert self.account is not None

        if self.settings.display_name:
            self.account.set_config("displayname", self.settings.display_name)

        can_configure_with_qr = self.settings.setup_qr != ""
        can_configure_with_credentials = self.settings.email != "" and self.settings.password != ""

        if not can_configure_with_qr and not can_configure_with_credentials:
            raise RuntimeError(
                "No Delta Chat account setup available. Set DELTACHAT_SETUP_QR or DELTACHAT_EMAIL+DELTACHAT_PASSWORD."
            )

        if can_configure_with_qr:
            self.account.set_config_from_qr(self.settings.setup_qr)

        if can_configure_with_credentials:
            self.account.set_config("addr", self.settings.email)
            self.account.set_config("mail_pw", self.settings.password)

        self.account.configure()

        deadline = time.time() + self.settings.configure_timeout_seconds
        while time.time() < deadline:
            event = self.account.wait_for_event()
            kind = str(event.get("kind", ""))

            if kind == EventType.CONFIGURE_PROGRESS.value:
                progress = int(event.get("progress", 0))
                logging.info("Configure progress: %d/1000", progress)
                if progress >= 1000:
                    break
                continue

            if kind == EventType.WARNING.value:
                logging.warning("Configure warning: %s", event.get("msg", ""))
                continue

            if kind == EventType.ERROR.value:
                raise RuntimeError(f"Delta Chat configure error: {event.get('msg', 'unknown error')}")

        if not self.account.is_configured():
            raise TimeoutError("Timed out waiting for Delta Chat account configuration to complete")

    def _start_deltachat(self) -> None:
        self._ensure_accounts_store()

        rpc = Rpc(accounts_dir=self.settings.accounts_dir, rpc_server_path=self.settings.rpc_server_path)
        rpc.start()
        self.rpc = rpc

        delta_chat = DeltaChat(rpc)
        self.delta_chat = delta_chat

        accounts = delta_chat.get_all_accounts()
        account = accounts[0] if accounts else delta_chat.add_account()
        self.account = account

        if not account.is_configured():
            logging.info("Delta Chat account is not configured yet, starting setup")
            self._configure_account()

        account.start_io()

        try:
            self._export_contact_qr(account)
        except Exception as exc:
            logging.warning("Failed to export Delta Chat contact QR: %s", exc)

        addr = account.get_config("addr") or "<unknown>"
        logging.info("Using Delta Chat identity: %s", addr)

    @staticmethod
    def _normalize_media_path(file_path: Any, accounts_dir: str) -> str:
        path = Path(str(file_path))
        if path.is_absolute():
            return str(path)

        path_str = str(path)
        blob_marker = "$BLOBDIR/"
        if blob_marker in path_str:
            blob_rel = path_str.split(blob_marker, 1)[1].lstrip("/\\")
            if blob_rel:
                matches = list(Path(accounts_dir).glob(f"*/dc.db-blobs/{blob_rel}"))
                for match in matches:
                    if match.exists():
                        return str(match.resolve())

        root_relative = (Path("/") / path).resolve()
        if root_relative.exists():
            return str(root_relative)

        account_relative = (Path(accounts_dir) / path).resolve()
        if account_relative.exists():
            return str(account_relative)

        return str(path)

    @staticmethod
    def _parse_snapshot_param_map(raw: Any) -> dict[str, str]:
        if raw is None:
            return {}

        if not isinstance(raw, str):
            raw = str(raw)

        fields: dict[str, str] = {}
        for line in raw.splitlines():
            line = line.strip()
            if line == "" or "=" not in line:
                continue
            key, value = line.split("=", 1)
            key = key.strip()
            if key == "":
                continue
            fields[key] = value.strip()

        return fields

    def _resolve_snapshot_media_path(self, raw_path: Any) -> str:
        normalized = self._normalize_media_path(raw_path, self.settings.accounts_dir)
        if normalized == "":
            return ""

        path = Path(normalized)
        if path.exists():
            return str(path)

        return ""

    def _lookup_media_path_from_db(self, message_id: int) -> str:
        if message_id <= 0:
            return ""

        accounts_root = Path(self.settings.accounts_dir)
        for db_path in accounts_root.glob("*/dc.db"):
            try:
                with sqlite3.connect(str(db_path)) as conn:
                    row = conn.execute("SELECT param FROM msgs WHERE id = ? LIMIT 1", (message_id,)).fetchone()
            except Exception:
                continue

            if not row:
                continue

            fields = self._parse_snapshot_param_map(row[0])
            blob_file = fields.get("f")
            if not blob_file:
                continue

            resolved = self._resolve_snapshot_media_path(blob_file)
            if resolved:
                return resolved

        return ""

    def _message_to_bridge_payload(self, message: Any) -> dict[str, Any] | None:
        snapshot = message.get_snapshot()

        if snapshot.from_id == SpecialContactId.SELF:
            return None
        if snapshot.get("is_bot", False) or snapshot.get("is_info", False):
            return None

        sender_snapshot = snapshot.sender.get_snapshot()
        sender = sender_snapshot.get("address") or str(snapshot.from_id)
        sender_name = sender_snapshot.get("display_name") or sender_snapshot.get("name") or sender

        payload: dict[str, Any] = {
            "type": "message",
            "id": str(snapshot.id),
            "from": sender,
            "from_name": sender_name,
            "chat": str(snapshot.chat_id),
            "content": snapshot.get("text") or "",
        }

        bridge_received_ms = int(time.time() * 1000)
        payload["bridge_received_ms"] = bridge_received_ms

        timestamp_ms = self._coerce_millis(snapshot.get("timestamp"))
        if timestamp_ms > 0:
            payload["dc_timestamp_ms"] = timestamp_ms

        timestamp_sent_ms = self._coerce_millis(snapshot.get("timestamp_sent"))
        if timestamp_sent_ms > 0:
            payload["dc_timestamp_sent_ms"] = timestamp_sent_ms

        timestamp_rcvd_ms = self._coerce_millis(snapshot.get("timestamp_rcvd"))
        if timestamp_rcvd_ms > 0:
            payload["dc_timestamp_rcvd_ms"] = timestamp_rcvd_ms

        media_paths: list[str] = []

        media = snapshot.get("file")
        if media:
            resolved = self._resolve_snapshot_media_path(media)
            if resolved:
                media_paths.append(resolved)

        params = self._parse_snapshot_param_map(snapshot.get("param"))
        blob_file = params.get("f")
        if blob_file:
            resolved = self._resolve_snapshot_media_path(blob_file)
            if resolved and resolved not in media_paths:
                media_paths.append(resolved)

        if not media_paths:
            message_id = 0
            try:
                message_id = int(snapshot.get("id") or 0)
            except (TypeError, ValueError):
                message_id = 0

            if message_id > 0:
                resolved = self._lookup_media_path_from_db(message_id)
                if resolved:
                    media_paths.append(resolved)

        if media_paths:
            payload["media"] = media_paths

        return payload

    @staticmethod
    def _coerce_bool(value: Any, default: bool = False) -> bool:
        if isinstance(value, bool):
            return value
        if isinstance(value, str):
            lowered = value.strip().lower()
            if lowered in {"1", "true", "yes", "on"}:
                return True
            if lowered in {"0", "false", "no", "off"}:
                return False
        if isinstance(value, (int, float)):
            return value != 0
        return default

    @staticmethod
    def _coerce_millis(value: Any) -> int:
        if value is None:
            return 0

        parsed: float | None = None

        if isinstance(value, (int, float)):
            parsed = float(value)
        elif isinstance(value, str):
            stripped = value.strip()
            if stripped == "":
                return 0
            try:
                parsed = float(stripped)
            except ValueError:
                return 0
        else:
            return 0

        if parsed is None or parsed <= 0:
            return 0

        # DeltaChat snapshot timestamps are usually epoch seconds. Bridge fields
        # use epoch milliseconds for easier latency calculations downstream.
        if parsed < 10_000_000_000:
            parsed *= 1000

        return int(parsed)

    def _reaction_event_to_bridge_payload(self, event: Any) -> dict[str, Any] | None:
        assert self.account is not None

        msg_id_raw = event.get("msg_id")
        if msg_id_raw is None:
            return None

        try:
            msg_id = int(msg_id_raw)
        except (TypeError, ValueError):
            return None

        if msg_id <= 0:
            return None

        message = self.account.get_message_by_id(msg_id)
        snapshot = message.get_snapshot()

        contact_id_raw = event.get("contact_id")
        reactor_contact_id = 0
        try:
            reactor_contact_id = int(contact_id_raw)
        except (TypeError, ValueError):
            reactor_contact_id = 0

        reactor_addr = ""
        reactor_name = ""
        if reactor_contact_id > 0:
            try:
                contact_snapshot = self.account.get_contact_by_id(reactor_contact_id).get_snapshot()
                reactor_addr = contact_snapshot.get("address") or ""
                reactor_name = contact_snapshot.get("display_name") or contact_snapshot.get("name") or ""
            except Exception:
                reactor_addr = ""

        if reactor_addr == "":
            sender_snapshot = snapshot.sender.get_snapshot()
            reactor_addr = sender_snapshot.get("address") or str(reactor_contact_id or snapshot.from_id)
            reactor_name = sender_snapshot.get("display_name") or sender_snapshot.get("name") or reactor_addr

        reactions_value = message.get_reactions()
        reaction_text = ""
        all_emojis: list[str] = []

        if reactions_value:
            by_contact = reactions_value.get("reactions_by_contact") or {}
            contact_emojis: list[str] = []
            if reactor_contact_id > 0:
                maybe = by_contact.get(str(reactor_contact_id))
                if isinstance(maybe, list):
                    contact_emojis = [str(item) for item in maybe if str(item).strip() != ""]

            if contact_emojis:
                reaction_text = " ".join(contact_emojis)
                all_emojis = contact_emojis
            else:
                merged: list[str] = []
                for item in reactions_value.get("reactions", []):
                    emoji = item.get("emoji") if isinstance(item, dict) else None
                    if emoji:
                        merged.append(str(emoji))
                if merged:
                    reaction_text = " ".join(merged)
                    all_emojis = merged

        payload: dict[str, Any] = {
            "type": "reaction",
            "id": str(snapshot.id),
            "message_id": str(snapshot.id),
            "from": reactor_addr,
            "from_name": reactor_name,
            "chat": str(snapshot.chat_id),
            "reaction": reaction_text,
        }
        if all_emojis:
            payload["reactions"] = all_emojis

        return payload

    def _reader_loop(self) -> None:
        assert self.account is not None

        incoming_kinds = {
            EventType.INCOMING_MSG.value,
            EventType.INCOMING_MSG_BUNCH.value,
            EventType.MSGS_CHANGED.value,
        }
        reaction_kinds = {
            EventType.INCOMING_REACTION.value,
            EventType.REACTIONS_CHANGED.value,
        }

        while not self.stop_event.is_set():
            try:
                event = self.account.wait_for_event()
                kind = str(event.get("kind", ""))

                if kind == EventType.INCOMING_MSG.value:
                    msg_id_raw = event.get("msg_id")
                    msg_id = 0
                    try:
                        msg_id = int(msg_id_raw)
                    except (TypeError, ValueError):
                        msg_id = 0

                    if msg_id > 0:
                        try:
                            message = self.account.get_message_by_id(msg_id)
                            payload = self._message_to_bridge_payload(message)
                            if payload is not None:
                                self.delta_to_ws_queue.put(payload)
                            try:
                                message.mark_seen()
                            except Exception:
                                pass
                            continue
                        except Exception as exc:
                            logging.debug("Failed to handle INCOMING_MSG via msg_id=%s, falling back to get_next_messages: %s", msg_id, exc)

                if kind in reaction_kinds:
                    payload = self._reaction_event_to_bridge_payload(event)
                    if payload is not None:
                        self.delta_to_ws_queue.put(payload)
                    continue

                if kind not in incoming_kinds:
                    continue

                for message in self.account.get_next_messages():
                    payload = self._message_to_bridge_payload(message)
                    if payload is None:
                        continue
                    self.delta_to_ws_queue.put(payload)
                    try:
                        message.mark_seen()
                    except Exception:
                        pass
            except Exception as exc:
                if self.stop_event.is_set():
                    break
                logging.error("Delta Chat event loop error: %s", exc)
                time.sleep(1)

    def _resolve_chat(self, target: str) -> Any:
        assert self.account is not None

        if target.isdigit():
            return self.account.get_chat_by_id(int(target))

        contact = self.account.create_contact(target)
        return contact.create_chat()

    def _resolve_outbound_file(self, file_path_raw: Any) -> str:
        file_path = str(file_path_raw).strip() if file_path_raw is not None else ""
        if file_path == "":
            raise ValueError("Missing file path")

        normalized = self._normalize_media_path(file_path, self.settings.accounts_dir)
        candidate = Path(normalized)
        if not candidate.exists():
            raise FileNotFoundError(f"file does not exist: {normalized}")

        return str(candidate)

    def _send_to_deltachat(self, payload: dict[str, Any]) -> None:
        target_raw = payload.get("to")
        target = str(target_raw).strip() if target_raw is not None else ""
        if target == "":
            raise ValueError("Missing 'to' in bridge message")

        chat = self._resolve_chat(target)
        try:
            chat.accept()
        except Exception:
            pass

        content = payload.get("content")
        if content is None:
            content = ""

        content_text = str(content)

        media = payload.get("media")
        media_items: list[str] = []
        if isinstance(media, list):
            media_items = [str(item) for item in media if isinstance(item, str)]

        logging.info(
            "Outbound payload to Delta Chat: chat=%s content_chars=%d media_count=%d",
            target,
            len(content_text),
            len(media_items),
        )

        if media_items:
            first_file = self._resolve_outbound_file(media_items[0])
            first_vt = ViewType.IMAGE if Path(first_file).suffix.lower() in _IMAGE_EXTENSIONS else None
            if content != "":
                chat.send_message(text=content, file=first_file, viewtype=first_vt)
            else:
                chat.send_message(file=first_file, viewtype=first_vt) if first_vt else chat.send_file(first_file)

            for extra_file in media_items[1:]:
                resolved = self._resolve_outbound_file(extra_file)
                vt = ViewType.IMAGE if Path(resolved).suffix.lower() in _IMAGE_EXTENSIONS else None
                chat.send_message(file=resolved, viewtype=vt) if vt else chat.send_file(resolved)

            logging.info(
                "Outbound payload accepted by Delta Chat: chat=%s content_chars=%d media_count=%d",
                target,
                len(content_text),
                len(media_items),
            )
            return

        if content != "":
            chat.send_text(content)
            logging.info(
                "Outbound payload accepted by Delta Chat: chat=%s content_chars=%d media_count=0",
                target,
                len(content_text),
            )
            return

        logging.warning("Ignoring empty outbound message for target %s", target)

    def _set_profile_image(self, payload: dict[str, Any]) -> None:
        assert self.account is not None

        path_raw = payload.get("path")
        if path_raw is None:
            raise ValueError("Missing 'path' in profile_image payload")

        path = self._resolve_outbound_file(path_raw)
        self.account.set_avatar(path)

    def _set_typing(self, payload: dict[str, Any]) -> None:
        target_raw = payload.get("to")
        target = str(target_raw).strip() if target_raw is not None else ""
        if target == "":
            raise ValueError("Missing 'to' in typing payload")

        chat = self._resolve_chat(target)
        is_typing = self._coerce_bool(payload.get("typing"), default=False)

        if is_typing:
            draft_text_raw = payload.get("content")
            draft_text = str(draft_text_raw).strip() if draft_text_raw is not None else ""
            if draft_text == "":
                draft_text = "..."
            chat.set_draft(text=draft_text)
            return

        chat.remove_draft()

    def _set_thinking(self, payload: dict[str, Any]) -> None:
        target_raw = payload.get("to")
        target = str(target_raw).strip() if target_raw is not None else ""
        if target == "":
            raise ValueError("Missing 'to' in thinking payload")

        chat = self._resolve_chat(target)
        payload_type = str(payload.get("type") or "").strip()

        if payload_type == "thinking_start":
            content_raw = payload.get("content")
            content = str(content_raw).strip() if content_raw is not None else ""
            if content == "":
                content = "thinking..."
            chat.set_draft(text=content)
            return

        chat.remove_draft()

    def _send_reaction(self, payload: dict[str, Any]) -> None:
        assert self.account is not None

        message_id_raw = payload.get("message_id")
        if message_id_raw is None:
            raise ValueError("Missing 'message_id' in reaction payload")

        try:
            message_id = int(str(message_id_raw).strip())
        except ValueError as exc:
            raise ValueError("Invalid 'message_id' in reaction payload") from exc

        reaction_raw = payload.get("reaction")
        if reaction_raw is None:
            raise ValueError("Missing 'reaction' in reaction payload")

        reaction_items: list[str]
        if isinstance(reaction_raw, list):
            reaction_items = [str(item).strip() for item in reaction_raw if str(item).strip() != ""]
        else:
            reaction_items = [str(reaction_raw).strip()]

        reaction_items = [item for item in reaction_items if item != ""]
        if not reaction_items:
            raise ValueError("Reaction payload contained no emoji")

        message = self.account.get_message_by_id(message_id)
        message.send_reaction(*reaction_items)

    async def _broadcast(self, payload: dict[str, Any]) -> None:
        data = json.dumps(payload, ensure_ascii=True)

        with self.clients_lock:
            clients = list(self.clients)

        if not clients:
            return

        to_remove: list[Any] = []
        for client in clients:
            try:
                await client.send(data)
            except Exception:
                to_remove.append(client)

        if to_remove:
            with self.clients_lock:
                for client in to_remove:
                    self.clients.discard(client)

    async def _pump_delta_messages(self) -> None:
        while not self.stop_event.is_set():
            payload = await asyncio.to_thread(self.delta_to_ws_queue.get)
            if payload is None:
                break
            await self._broadcast(payload)

    async def _send_ws_ack(
        self,
        websocket: Any,
        request_id: str,
        payload_type: str,
        ok: bool,
        error_text: str = "",
    ) -> None:
        request_id = request_id.strip()
        if request_id == "":
            return

        ack_payload: dict[str, Any] = {
            "type": "ack",
            "request_id": request_id,
            "ok": ok,
        }
        payload_type = payload_type.strip()
        if payload_type != "":
            ack_payload["op"] = payload_type

        error_text = error_text.strip()
        if error_text != "":
            ack_payload["error"] = error_text

        try:
            await websocket.send(json.dumps(ack_payload, ensure_ascii=True))
            logging.info("Bridge ack sent: request_id=%s op=%s ok=%s", request_id, payload_type or "payload", ok)
        except Exception as exc:
            logging.error("Failed to send Delta Chat bridge ack: %s", exc)

    async def _handle_ws_message(self, websocket: Any, raw_data: str) -> None:
        try:
            payload = json.loads(raw_data)
        except json.JSONDecodeError:
            logging.warning("Dropped non-JSON websocket payload")
            return

        if not isinstance(payload, dict):
            logging.warning("Dropped websocket payload with non-object JSON")
            return

        payload_type = payload.get("type")
        request_id_raw = payload.get("request_id")
        request_id = str(request_id_raw).strip() if request_id_raw is not None else ""
        require_ack = self._coerce_bool(payload.get("require_ack"), default=False) and request_id != ""

        if payload_type == "message":
            try:
                await asyncio.to_thread(self._send_to_deltachat, payload)
            except Exception as exc:
                logging.error("Failed to send message to Delta Chat: %s", exc)
                if require_ack:
                    await self._send_ws_ack(websocket, request_id, "message", False, str(exc))
                return
            if require_ack:
                await self._send_ws_ack(websocket, request_id, "message", True)
            return

        if payload_type == "typing":
            try:
                await asyncio.to_thread(self._set_typing, payload)
            except Exception as exc:
                logging.error("Failed to update Delta Chat typing state: %s", exc)
                if require_ack:
                    await self._send_ws_ack(websocket, request_id, "typing", False, str(exc))
                return
            if require_ack:
                await self._send_ws_ack(websocket, request_id, "typing", True)
            return

        if payload_type in {"thinking_start", "thinking_clear"}:
            try:
                await asyncio.to_thread(self._set_thinking, payload)
            except Exception as exc:
                logging.error("Failed to update Delta Chat thinking marker: %s", exc)
                if require_ack:
                    await self._send_ws_ack(websocket, request_id, str(payload_type), False, str(exc))
                return
            if require_ack:
                await self._send_ws_ack(websocket, request_id, str(payload_type), True)
            return

        if payload_type == "reaction":
            try:
                await asyncio.to_thread(self._send_reaction, payload)
            except Exception as exc:
                logging.error("Failed to send Delta Chat reaction: %s", exc)
                if require_ack:
                    await self._send_ws_ack(websocket, request_id, "reaction", False, str(exc))
                return
            if require_ack:
                await self._send_ws_ack(websocket, request_id, "reaction", True)
            return

        if payload_type == "profile_image":
            try:
                await asyncio.to_thread(self._set_profile_image, payload)
            except Exception as exc:
                logging.error("Failed to set Delta Chat profile image: %s", exc)
                if require_ack:
                    await self._send_ws_ack(websocket, request_id, "profile_image", False, str(exc))
                return
            if require_ack:
                await self._send_ws_ack(websocket, request_id, "profile_image", True)
            return

        if require_ack:
            await self._send_ws_ack(websocket, request_id, str(payload_type), False, "unsupported payload type")

    async def _ws_handler(self, websocket: Any) -> None:
        peer = websocket.remote_address
        logging.info("WebSocket client connected: %s", peer)
        with self.clients_lock:
            self.clients.add(websocket)

        try:
            async for raw_data in websocket:
                if isinstance(raw_data, bytes):
                    raw_data = raw_data.decode("utf-8", errors="replace")
                await self._handle_ws_message(websocket, raw_data)
        except ConnectionClosed:
            pass
        finally:
            with self.clients_lock:
                self.clients.discard(websocket)
            logging.info("WebSocket client disconnected: %s", peer)

    async def run(self) -> None:
        self._set_ready_flag(False)
        self._start_deltachat()

        self.reader_thread = threading.Thread(target=self._reader_loop, name="deltachat-reader", daemon=True)
        self.reader_thread.start()

        pump_task = asyncio.create_task(self._pump_delta_messages())

        async with websockets.serve(
            self._ws_handler,
            self.settings.host,
            self.settings.port,
            ping_interval=20,
            ping_timeout=20,
            max_size=10 * 1024 * 1024,
        ):
            logging.info("DeltaChat bridge listening on ws://%s:%d", self.settings.host, self.settings.port)
            self._set_ready_flag(True)
            await asyncio.to_thread(self.stop_event.wait)

        self._set_ready_flag(False)

        with self.clients_lock:
            clients = list(self.clients)

        for client in clients:
            try:
                await client.close()
            except Exception:
                pass

        if not pump_task.done():
            self.delta_to_ws_queue.put(None)
            await pump_task


def install_signal_handlers(bridge: DeltaChatBridge) -> None:
    loop = asyncio.get_running_loop()

    for sig in (signal.SIGINT, signal.SIGTERM):
        try:
            loop.add_signal_handler(sig, bridge.stop)
        except NotImplementedError:
            signal.signal(sig, lambda _sig, _frame: bridge.stop())


async def run_bridge(bridge: DeltaChatBridge) -> None:
    install_signal_handlers(bridge)
    try:
        await bridge.run()
    finally:
        bridge.stop()


def main() -> None:
    settings = Settings.from_env()
    logging.basicConfig(
        level=getattr(logging, settings.log_level, logging.INFO),
        format="%(asctime)s %(levelname)s %(message)s",
    )

    bridge = DeltaChatBridge(settings)
    asyncio.run(run_bridge(bridge))


if __name__ == "__main__":
    main()
