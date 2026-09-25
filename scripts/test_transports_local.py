#!/usr/bin/env python3
"""E2E verification of MCP Gateway Adapter (stdio & Streamable HTTP) and Core Security."""

import json
import os
import subprocess
import sys
import time
import urllib.request
import urllib.error

DEFAULT_ADAPTER_BIN = os.path.abspath(
    os.path.join(os.path.dirname(__file__), "..", "mcp-adapter", "mcp-gateway-adapter")
)
ADAPTER_BIN = os.environ.get("MCP_ADAPTER_BIN", DEFAULT_ADAPTER_BIN)
SRC_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "src"))
if SRC_DIR not in sys.path:
    sys.path.insert(0, SRC_DIR)

from mcp_gateway import compatibility
from mcp_gateway.bridge import get_tools_catalog

EXPECTED_TOOLS = get_tools_catalog()
_MCP_COMPAT = compatibility.get_compatibility().get("mcp", {})
EXPECTED_PROTOCOLS = {
    value
    for value in (
        _MCP_COMPAT.get("protocol"),
        _MCP_COMPAT.get("protocol_legacy"),
    )
    if value
}


HTTP_ACCEPT = "application/json, text/event-stream"


def _decode_mcp_http_body(body):
    stripped = body.strip()
    if not stripped:
        raise AssertionError("MCP HTTP response body is empty")
    if stripped.startswith("{"):
        return json.loads(stripped)

    data_lines = [
        line[len("data:"):].lstrip()
        for line in stripped.splitlines()
        if line.startswith("data:")
    ]
    if not data_lines:
        raise AssertionError(f"Unsupported MCP HTTP response format: {body!r}")
    return json.loads("\n".join(data_lines))


def _stop_process(process):
    if process.poll() is not None:
        return
    process.terminate()
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)


def test_stdio():
    print("=== TEST STDIO TRANSPORT ===")
    p = subprocess.Popen(
        [ADAPTER_BIN, "-transport", "stdio", "-python", sys.executable, "-pythonpath", SRC_DIR],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    try:
        def send_recv(msg):
            p.stdin.write(json.dumps(msg) + "\n")
            p.stdin.flush()
            line = p.stdout.readline()
            return json.loads(line) if line else None

        init_msg = {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "initialize",
            "params": {
                "protocolVersion": "2026-07-28",
                "capabilities": {},
                "clientInfo": {"name": "test-stdio", "version": "1.0.0"},
            },
        }
        res_init = send_recv(init_msg)
        print("Init response:", json.dumps(res_init, indent=2))
        assert res_init and "result" in res_init, f"Init failed: {res_init}"

        p.stdin.write(
            json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}) + "\n"
        )
        p.stdin.flush()

        list_msg = {"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}}
        res_list = send_recv(list_msg)
        tools = res_list.get("result", {}).get("tools", [])
        tool_names = [t["name"] for t in tools]
        print(f"Discovered {len(tools)} tools: {tool_names}")
        assert tool_names == EXPECTED_TOOLS, (
            f"Adapter catalog diverged from Core catalog: expected {EXPECTED_TOOLS}, got {tool_names}"
        )

        call_health = {
            "jsonrpc": "2.0",
            "id": 3,
            "method": "tools/call",
            "params": {"name": "health", "arguments": {}},
        }
        t0 = time.monotonic()
        res_health = send_recv(call_health)
        latency_ms = (time.monotonic() - t0) * 1000
        print(f"Health call ({latency_ms:.1f}ms):", json.dumps(res_health, indent=2))
        assert res_health and "result" in res_health
        assert not res_health["result"].get("isError", False)
        print("Stdio test passed!\n")
    finally:
        _stop_process(p)


def test_http():
    print("=== TEST STREAMABLE HTTP TRANSPORT ===")
    port = 18092
    p = subprocess.Popen(
        [
            ADAPTER_BIN,
            "-transport",
            "http",
            "-bind",
            f"127.0.0.1:{port}",
            "-python",
            sys.executable,
            "-pythonpath",
            SRC_DIR,
        ],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    try:
        time.sleep(0.5)

        url_health = f"http://127.0.0.1:{port}/health"
        req = urllib.request.Request(url_health)
        with urllib.request.urlopen(req) as resp:
            body = resp.read().decode()
            print("GET /health:", body)
            assert resp.status == 200

        url_mcp = f"http://127.0.0.1:{port}/mcp"
        init_payload = json.dumps(
            {
                "jsonrpc": "2.0",
                "id": 1,
                "method": "initialize",
                "params": {
                    "protocolVersion": "2026-07-28",
                    "capabilities": {},
                    "clientInfo": {"name": "test-http", "version": "1.0.0"},
                },
            }
        ).encode()

        headers = {
            "Content-Type": "application/json",
            "Accept": HTTP_ACCEPT,
        }
        req_init = urllib.request.Request(url_mcp, data=init_payload, headers=headers)
        try:
            with urllib.request.urlopen(req_init) as resp:
                body = resp.read().decode()
                print("POST /mcp initialize response:", body)
                assert resp.status == 200
                init_response = _decode_mcp_http_body(body)
                assert "result" in init_response
                negotiated = init_response["result"].get("protocolVersion")
                assert negotiated in EXPECTED_PROTOCOLS, (
                    f"Unexpected negotiated protocol {negotiated!r}; expected one of {sorted(EXPECTED_PROTOCOLS)}"
                )
        except urllib.error.HTTPError as exc:
            print("HTTP Error:", exc.code, exc.read().decode())
            raise

        call_payload = json.dumps(
            {
                "jsonrpc": "2.0",
                "id": 2,
                "method": "tools/call",
                "params": {"name": "health", "arguments": {}},
            }
        ).encode()

        t0 = time.monotonic()
        req_call = urllib.request.Request(url_mcp, data=call_payload, headers=headers)
        with urllib.request.urlopen(req_call) as resp:
            body = resp.read().decode()
            latency_ms = (time.monotonic() - t0) * 1000
            print(f"POST /mcp tools/call health ({latency_ms:.1f}ms):", body)
            assert resp.status == 200
            data = _decode_mcp_http_body(body)
            assert "result" in data
            assert not data["result"].get("isError", False)

        print("HTTP test passed!\n")
    finally:
        _stop_process(p)


if __name__ == "__main__":
    test_stdio()
    test_http()
