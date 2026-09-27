# OpenAI Tunnel-Client Provenance & 32-bit Compilation Guide

This document records the exact provenance, upstream source commit, compilation environment, patches, and artifact checksums for the official OpenAI Secure MCP Tunnel client compiled for the Raspberry Pi Model A+ (`armv6l` / 32-bit ARM).

---

## 1. Upstream Source & Provenance

- **Upstream Repository**: `https://github.com/openai/tunnel-client.git`
- **Upstream Commit**: `3b706ea54d0ad303c85d5ccd35633ae69405570b`
- **Upstream Author**: `Copyberry <copyberry@app.openai.com>`
- **Upstream Date**: `2026-09-10 14:25:01 -0700`
- **License**: Apache-2.0

---

## 2. 32-Bit Architecture Rationale & Minimal Patch

### The Issue
In Go on 32-bit architectures (`armv6l`, `armhf`, `386`), `uint` is 32-bit with a maximum value of `4,294,967,295`. Upstream `pkg/runtimeconfig/config.go` contained compile-time constant assertions using `uint(...)` against nanosecond durations:
```go
const _ = uint(maxControlPlanePollDeadline - defaultControlPlanePollTimeout - defaultControlPlanePollDeadlineGuardrail)
```
Because `maxControlPlanePollDeadline` is on the order of 5 minutes (`300,000,000,000` nanoseconds), this causes a compile-time overflow error when targeting 32-bit systems:
```text
constant 290000000000 overflows uint
```

### The Minimal Patch
The patch changes `uint(...)` to `uint64(...)` for these assertion guards. This eliminates the 32-bit constant overflow while preserving identical compile-time guard semantics on both 32-bit and 64-bit systems.

The patch is archived in the repository at:
`patches/0001-fix-32bit-uint-overflow.patch`

---

## 3. Build Environment & Commands

- **Compiler**: Go 1.27 (`go version go1.27.0 windows/amd64` or cross-compiler)
- **Target OS**: `linux`
- **Target Architecture**: `arm`
- **Target ARM Version**: `6` (`armv6l` for BCM2835 Raspberry Pi Model A+)

### Build Command
```bash
git clone https://github.com/openai/tunnel-client.git
cd tunnel-client
git checkout 3b706ea54d0ad303c85d5ccd35633ae69405570b
git apply ../patches/0001-fix-32bit-uint-overflow.patch

env GOOS=linux GOARCH=arm GOARM=6 CGO_ENABLED=0 go build -ldflags="-s -w" -o tunnel-client-linux-armv6 ./cmd/client
```

---

## 4. Deployed Binary & Cryptographic Checksum

| Property | Value |
|---|---|
| Target Appliance Path | `/usr/local/bin/openai-tunnel-client` |
| Target Permissions | `0755 root:root` |
| Target Architecture | ELF 32-bit LSB executable, ARM, EABI5 version 1 (SYSV), statically linked |
| SHA-256 Checksum | `7788e773697dcade6f717aa42acf14cd8531282e5ee8b7822b2aace83c7fd6eb` |

---

## 5. Security & Trust Boundary

- The tunnel client runs under the dedicated unprivileged system user `mcp-gateway`.
- Outbound only (no open ingress router ports).
- Injects a pre-shared cryptographic Bearer token via `--mcp.extra-headers` loaded from `/home/mcp-gateway/.config/mcp-gateway/tunnel-mcp.token` (permissions `0600`).
