#!/usr/bin/env python3

"""Manual Anthropic OAuth helper.

This starts a PKCE OAuth flow that opens the browser, asks the user to paste the
authorization code shown by Anthropic, exchanges it for an access token, and
optionally creates a regular Anthropic API key.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
import shutil
import subprocess
import sys
import textwrap
import urllib.error
import urllib.parse
import urllib.request


CLIENT_ID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
CLAUDE_AI_AUTHORIZE_URL = "https://claude.ai/oauth/authorize"
CONSOLE_AUTHORIZE_URL = "https://platform.claude.com/oauth/authorize"
TOKEN_URL = "https://platform.claude.com/v1/oauth/token"
REDIRECT_URI = "https://platform.claude.com/oauth/code/callback"
CREATE_API_KEY_URL = "https://api.anthropic.com/api/oauth/claude_cli/create_api_key"
SCOPES = "org:create_api_key user:profile user:inference"
USER_AGENT = "claude-cli/2.1.15 (external, cli)"


def base64url_nopad(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).decode("ascii").rstrip("=")


def generate_pkce() -> tuple[str, str]:
    verifier = base64url_nopad(os.urandom(48))
    challenge = base64url_nopad(hashlib.sha256(verifier.encode("ascii")).digest())
    return verifier, challenge


def build_authorize_url(mode: str, state: str, code_challenge: str) -> str:
    base = CLAUDE_AI_AUTHORIZE_URL if mode == "claudeai" else CONSOLE_AUTHORIZE_URL
    params = {
        "code": "true",
        "client_id": CLIENT_ID,
        "response_type": "code",
        "redirect_uri": REDIRECT_URI,
        "scope": SCOPES,
        "code_challenge": code_challenge,
        "code_challenge_method": "S256",
        "state": state,
    }
    return base + "?" + urllib.parse.urlencode(params, quote_via=urllib.parse.quote)


def maybe_open_browser(url: str) -> bool:
    commands: list[list[str]] = []
    if sys.platform == "darwin":
        commands.append(["open", url])
    elif sys.platform.startswith("linux") and shutil.which("xdg-open"):
        commands.append(["xdg-open", url])
    elif sys.platform.startswith("win"):
        commands.append(["cmd", "/c", "start", "", url])

    for cmd in commands:
        try:
            subprocess.run(cmd, check=False, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            return True
        except OSError:
            continue
    return False


def parse_manual_code(raw: str, default_state: str) -> tuple[str, str]:
    value = raw.strip()
    if not value:
        raise ValueError("no code received")

    if value.startswith("http://") or value.startswith("https://"):
        parsed = urllib.parse.urlparse(value)
        query = urllib.parse.parse_qs(parsed.query)
        fragment = urllib.parse.parse_qs(parsed.fragment)
        code = first_nonempty(query.get("code"), fragment.get("code"))
        state = first_nonempty(query.get("state"), fragment.get("state"), [default_state])
        if code:
            return code, state

    if "#" in value:
        code, state = value.split("#", 1)
        code = code.strip()
        state = state.strip() or default_state
        if code:
            return code, state

    return value, default_state


def first_nonempty(*values: list[str] | None) -> str:
    for value in values:
        if value:
            for item in value:
                item = item.strip()
                if item:
                    return item
    return ""


def post_json(url: str, payload: dict[str, object], headers: dict[str, str] | None = None) -> dict[str, object]:
    encoded = json.dumps(payload).encode("utf-8")
    request_headers = {
        "content-type": "application/json",
        "accept": "application/json",
        "user-agent": USER_AGENT,
    }
    if headers:
        request_headers.update(headers)
    request = urllib.request.Request(url, data=encoded, method="POST", headers=request_headers)
    try:
        with urllib.request.urlopen(request, timeout=60) as response:
            return json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", "replace")
        raise RuntimeError(f"{exc.code} {exc.reason}: {body}") from exc


def exchange_code(code: str, state: str, verifier: str) -> dict[str, object]:
    return post_json(
        TOKEN_URL,
        {
            "code": code,
            "state": state,
            "grant_type": "authorization_code",
            "client_id": CLIENT_ID,
            "redirect_uri": REDIRECT_URI,
            "code_verifier": verifier,
        },
    )


def create_api_key(access_token: str) -> str:
    payload = post_json(
        CREATE_API_KEY_URL,
        {},
        headers={"authorization": f"Bearer {access_token}"},
    )
    raw_key = str(payload.get("raw_key", "")).strip()
    if not raw_key:
        raise RuntimeError(f"create_api_key returned no raw_key: {json.dumps(payload)}")
    return raw_key


def copy_to_clipboard(value: str) -> bool:
    commands = []
    if shutil.which("pbcopy"):
        commands.append(["pbcopy"])
    if shutil.which("xclip"):
        commands.append(["xclip", "-selection", "clipboard"])
    if shutil.which("xsel"):
        commands.append(["xsel", "--clipboard", "--input"])

    for cmd in commands:
        try:
            subprocess.run(cmd, input=value.encode("utf-8"), check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            return True
        except (OSError, subprocess.CalledProcessError):
            continue
    return False


def main() -> int:
    parser = argparse.ArgumentParser(description="Manual Anthropic OAuth helper")
    parser.add_argument(
        "--mode",
        choices=["claudeai", "console"],
        default="claudeai",
        help="Use claude.ai subscription auth or console auth (default: claudeai)",
    )
    parser.add_argument(
        "--api-key",
        action="store_true",
        help="Create and print a normal Anthropic API key instead of the OAuth access token",
    )
    parser.add_argument(
        "--copy",
        action="store_true",
        help="Copy the final token or API key to the clipboard if possible",
    )
    parser.add_argument(
        "--no-open",
        action="store_true",
        help="Do not try to open the browser automatically",
    )
    args = parser.parse_args()

    verifier, challenge = generate_pkce()
    state = base64url_nopad(os.urandom(24))
    url = build_authorize_url(args.mode, state, challenge)

    print()
    print("Anthropic manual OAuth flow")
    print("- mode:", args.mode)
    print("- output:", "API key" if args.api_key else "OAuth access token")
    print()

    opened = False
    if not args.no_open:
        opened = maybe_open_browser(url)
    if opened:
        print("Opened browser for sign-in.")
    else:
        print("Open this URL in your browser:")
        print(url)
    print()
    if opened:
        print("If the browser did not open, use this URL:")
        print(url)
        print()

    print(textwrap.fill(
        "After approving access, Anthropic should show a copyable authorization code. "
        "Paste either the raw code, code#state, or the full callback URL below.",
        width=88,
    ))
    print()

    try:
        manual = input("Paste code here: ").strip()
        code, returned_state = parse_manual_code(manual, state)
        token_payload = exchange_code(code, returned_state, verifier)
    except KeyboardInterrupt:
        print("\nCancelled.")
        return 130
    except Exception as exc:
        print(f"\nToken exchange failed: {exc}", file=sys.stderr)
        return 1

    access_token = str(token_payload.get("access_token", "")).strip()
    if not access_token:
        print(f"\nToken exchange returned no access_token: {json.dumps(token_payload)}", file=sys.stderr)
        return 1

    secret = access_token
    label = "OAuth access token"
    if args.api_key:
        try:
            secret = create_api_key(access_token)
            label = "Anthropic API key"
        except Exception as exc:
            print(f"\nAPI key creation failed: {exc}", file=sys.stderr)
            return 1

    print()
    print(label + ":")
    print(secret)

    if args.copy:
        if copy_to_clipboard(secret):
            print("\nCopied to clipboard.")
        else:
            print("\nCould not copy to clipboard automatically.")

    refresh_token = str(token_payload.get("refresh_token", "")).strip()
    if refresh_token and not args.api_key:
        print("\nRefresh token was also returned but is not shown.")

    if not args.api_key:
        print("Use this with Bearer auth plus the Anthropic OAuth beta header.")
    else:
        print("Use this with the normal x-api-key Anthropic auth flow.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
