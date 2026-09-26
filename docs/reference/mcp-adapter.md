# Adaptador Oficial de Protocolo MCP

Este documento describe la especificación técnica, arquitectura, transporte y modelo de seguridad del **Adaptador Oficial de Protocolo MCP (Model Context Protocol)** implementado en MCP-Pi.

---

## 1. Visión General y Propósito

El adaptador MCP actúa como una capa de serialización y transporte agnóstica del cliente, construida directamente sobre el **SDK Oficial de Go** (`github.com/modelcontextprotocol/go-sdk` v1.7.0). Su función es exponer el catálogo determinista actual de 21 herramientas del Gateway Core hacia clientes MCP mediante una interfaz formal y estandarizada, sin duplicar la lógica sensible del Policy Engine en el propio adaptador.

```text
[ Cliente MCP (Claude Desktop, IDE, etc.) ]
                    │
                    │ JSON-RPC 2.0 (stdio o Streamable HTTP)
                    ▼
┌────────────────────────────────────────────────────────┐
│ MCP Gateway Adapter (Go SDK v1.7.0, ARMv6)             │
│ - Protocolo MCP 2026-07-28 (fallback 2025-11-25)       │
│ - Transports: Stdio & Streamable HTTP (127.0.0.1:8090) │
│ - Schemas estrictos del catálogo determinista                  │
└───────────────────────────┬────────────────────────────┘
                            │
                            │ Subprocess invocations (CLI Bridge)
                            │ python3 -m mcp_gateway.bridge invoke <tool> <args>
                            ▼
┌────────────────────────────────────────────────────────┐
│ Python Gateway Core (tools.py + policy.py)             │
│ - SQLite Registry & Emergency Kill Switch              │
│ - Validación sintáctica y resolución canónica remota   │
│ - Lista blanca estricta de tareas por proyecto         │
│ - Ejecución SSH hacia target (`pc-local`)              │
└────────────────────────────────────────────────────────┘
```

---

## 2. Separación Estricta de Responsabilidades

| Componente | Qué HACE | Qué NO HACE |
|---|---|---|
| **Go MCP Adapter** | Handshake de protocolo MCP, exposición de esquemas de herramientas, transporte stdio y Streamable HTTP, serialización/deserialización JSON-RPC. | **NO** ejecuta SSH, **NO** valida rutas en disco, **NO** evalúa políticas de target o proyecto, **NO** decide el kill switch, **NO** accede directamente a la base de datos SQLite. |
| **Python Gateway Core** | Autoridad única de seguridad, consulta de base de datos SQLite, comprobación de kill switch global y por target, validación canónica de rutas mediante `os.path.realpath()` en el Target, filtrado de tareas y transporte SSH. | No gestiona la sesión ni el transporte HTTP/SSE del protocolo MCP directamente. |

---

## 3. Especificación Técnica

- **SDK Oficial**: `github.com/modelcontextprotocol/go-sdk`
- **Versión Fijada**: `v1.7.0` (definida en `mcp-adapter/go.mod`)
- **Especificación de Protocolo**: `2026-07-28` (con compatibilidad de inicialización `2025-11-25`)
- **Cross-Compilation**:
  - `GOOS=linux`
  - `GOARCH=arm`
  - `GOARM=6`
  - `CGO_ENABLED=0`
  - Flags de enlace: `-ldflags="-s -w"` (estático, binario de 8.38 MB sin dependencias de libc dinámico)
- **Ejecución en Raspberry Pi**: Nativa bajo arquitectura `armv6l` (BCM2835, ARMv6-compatible processor rev 7).

---

## 4. Catálogo de Herramientas Expuestas (Tool Schemas)

El adaptador expone el catálogo Core actual de **21 herramientas**, filtrado por grants para cada cliente:

- **Lectura/introspección:** `health`, `list_targets`, `target_status`, `list_directory`, `file_stat`, `read_file`, `git_status`, `search`.
- **Tareas/ejecución:** `run_task`, `run_command`.
- **Mutaciones estructuradas:** `write_file`, `append_file`, `delete_file`, `copy_file`, `move_file`, `mkdir`.
- **Administración del appliance:** `gateway_status`, `gateway_doctor`, `gateway_backup`, `gateway_maintenance`, `gateway_reboot`.

`run_command` conserva un único schema y admite `privilege=standard|required`. `required` no crea un bypass: el Core exige el acceso ordinario a trusted shell, un grant explícito `target_admin`, la `privilege_policy` del Target, cualquier aprobación necesaria y un backend elevado verificado. El adaptador Go sólo transporta esa intención; toda la decisión permanece en el Core Python.

---

## 5. Modos de Transporte

### 5.1 Stdio (`-transport stdio`)
- Diseñado para integración directa como subproceso local o en pruebas de conformidad CLI.
- No requiere sockets abiertos ni servicios en segundo plano.
- Ejecuta el protocolo JSON-RPC delimitado por saltos de línea sobre standard input/output.

### 5.2 Streamable HTTP (`-transport http`)
- Escucha exclusivamente en la interfaz de bucle invertido: `http://127.0.0.1:8090/mcp`.
- **Modo Stateless**: Configurado con `Stateless: true`, alineado con la evolución del protocolo MCP.
- **Cabeceras Obligatorias del Cliente**:
  - `Content-Type: application/json`
  - `Accept: application/json, text/event-stream`
- **Endpoint de Monitoreo**: `GET http://127.0.0.1:8090/health` retorna un estado estructurado con `gateway`, `ready`, `adapter_status`, metadatos MCP, `version_info` y la salud observada del Core.
- **Servicio del Sistema**: Administrado por systemd bajo el servicio `mcp-gateway-mcp.service` ejecutado como usuario `mcp-gateway` (UID 1001, sin sudo).

---

## 6. Puente con el Gateway Core (`mcp_gateway.bridge`)

El adaptador invoca al núcleo Python mediante subprocesos aislados:
```bash
python3 -m mcp_gateway.bridge invoke <tool_name> '<json_arguments>'
```
- Cada invocación inicia actualmente un proceso Python nuevo que carga el bridge/Core y abre el Registry existente.
- Esta frontera proceso-por-invocación es un candidato explícito de optimización en ARMv6. No se mantiene una cifra fija de latencia en esta referencia porque depende del runtime y del hardware desplegado; la medición autoritativa se obtiene con [`performance.md`](performance.md) y `scripts/benchmark_runtime.py`.
- Respeta de inmediato el **Emergency Kill Switch**: si `gateway_enabled` es falso en la base de datos SQLite, la llamada es rechazada con el código estandarizado `GATEWAY_DISABLED`.

---

## 7. Identidad del Cliente y Limitaciones Actuales

- **Identidad efectiva**: se configura con `--client-id` / `MCP_CLIENT_ID`; cuando existe autenticación HTTP sin un ID explícito, el runtime actual usa `chatgpt-main` como identidad configurada por defecto.
- **Autenticación HTTP**: el Bearer token se carga preferentemente desde archivo (`--auth-token-file` / `MCP_AUTH_TOKEN_FILE`) o desde la configuración equivalente. Un `X-MCP-Client-ID` solo se acepta con token válido y debe coincidir con la identidad configurada; de lo contrario se rechaza como spoofing.
- **Anonymous/fail-closed**: cuando hay autenticación configurada, las peticiones no autenticadas reciben un servidor con identidad `NONE` y catálogo vacío. `clientInfo` del protocolo no sustituye autenticación.

---

## 8. Integración con ChatGPT

> [!WARNING]
> **Aislamiento de Red y ChatGPT**:
> ChatGPT no puede conectarse de manera directa a un servidor MCP confinado exclusivamente a `127.0.0.1:8090`. Por principios de diseño y seguridad, el puerto 8090 **NO** debe exponerse a la LAN ni a Internet mediante port forwarding o servicios no auditados.
> El acceso externo debe mantenerse detrás del túnel/conector autenticado previsto por el despliegue. El puerto local `8090` no debe exponerse directamente a la LAN o Internet.
