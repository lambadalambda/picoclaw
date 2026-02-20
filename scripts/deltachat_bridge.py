#!/usr/bin/env python3

from __future__ import annotations

import asyncio
import json
import logging
import os
import signal
import threading
import time
from dataclasses import dataclass
from pathlib import Path
from queue import Queue
from typing import Any

import websockets
from deltachat_rpc_client import DeltaChat, EventType, Rpc, SpecialContactId
from websockets.exceptions import ConnectionClosed

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

        root_relative = (Path("/") / path).resolve()
        if root_relative.exists():
            return str(root_relative)

        account_relative = (Path(accounts_dir) / path).resolve()
        if account_relative.exists():
            return str(account_relative)

        return str(path)

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

        media = snapshot.get("file")
        if media:
            payload["media"] = [self._normalize_media_path(media, self.settings.accounts_dir)]

        return payload

    def _reader_loop(self) -> None:
        assert self.account is not None

        incoming_kinds = {
            EventType.INCOMING_MSG.value,
            EventType.INCOMING_MSG_BUNCH.value,
            EventType.MSGS_CHANGED.value,
        }

        while not self.stop_event.is_set():
            try:
                event = self.account.wait_for_event()
                if str(event.get("kind", "")) not in incoming_kinds:
                    continue

                for message in self.account.get_next_messages():
                    payload = self._message_to_bridge_payload(message)
                    if payload is None:
                        continue
                    try:
                        message.mark_seen()
                    except Exception:
                        pass
                    self.delta_to_ws_queue.put(payload)
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

        media = payload.get("media")
        media_items: list[str] = []
        if isinstance(media, list):
            media_items = [str(item) for item in media if isinstance(item, str)]

        if media_items:
            first_file = media_items[0]
            if content != "":
                chat.send_message(text=content, file=first_file)
            else:
                chat.send_file(first_file)

            for extra_file in media_items[1:]:
                chat.send_file(extra_file)
            return

        if content != "":
            chat.send_text(content)
            return

        logging.warning("Ignoring empty outbound message for target %s", target)

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

    async def _handle_ws_message(self, raw_data: str) -> None:
        try:
            payload = json.loads(raw_data)
        except json.JSONDecodeError:
            logging.warning("Dropped non-JSON websocket payload")
            return

        if not isinstance(payload, dict):
            logging.warning("Dropped websocket payload with non-object JSON")
            return

        if payload.get("type") != "message":
            return

        try:
            await asyncio.to_thread(self._send_to_deltachat, payload)
        except Exception as exc:
            logging.error("Failed to send message to Delta Chat: %s", exc)

    async def _ws_handler(self, websocket: Any) -> None:
        peer = websocket.remote_address
        logging.info("WebSocket client connected: %s", peer)
        with self.clients_lock:
            self.clients.add(websocket)

        try:
            async for raw_data in websocket:
                if isinstance(raw_data, bytes):
                    raw_data = raw_data.decode("utf-8", errors="replace")
                await self._handle_ws_message(raw_data)
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
