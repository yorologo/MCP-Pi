# Adaptador Oficial de Protocolo MCP (Phase 4C)

Este documento describe la especificación técnica, arquitectura, transporte y modelo de seguridad del **Adaptador Oficial de Protocolo MCP (Model Context Protocol)** implementado en MCP-Pi.

---

## 1. Visión General y Propósito

El adaptador MCP actúa como una capa de serialización y transporte agnóstica del cliente, construida directamente sobre el **SDK Oficial de Go** (`github.com/modelcontextprotocol/go-sdk` v1.7.0). Su función es exponer el catálogo de 8 herramientas seguras del Gateway Core hacia clientes MCP mediante una interfaz formal y estandarizada, sin ejecutar nunca lógica sensible en el propio adaptador.

```text
[ Cliente MCP (Claude Desktop, IDE, etc.) ]
                    │
                    │ JSON-RPC 2.0 (stdio o Streamable HTTP)
                    ▼
┌────────────────────────────────────────────────────────┐
│ MCP Gateway Adapter (Go SDK v1.7.0, ARMv6)             │
│ - Protocolo MCP 2026-07-28 (fallback 2025-11-25)       │
│ - Transports: Stdio & Streamable HTTP (127.0.0.1:8090) │
│ - Schemas estrictos de 8 herramientas                  │
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

El adaptador expone exactamente 8 herramientas con esquemas reducidos al mínimo privilegio:

1. **`health`**: Metadatos de salud, hostname, versión y conteo de targets configurados.
2. **`list_targets`**: Lista saneada de targets y proyectos disponibles (sin secretos de red).
3. **`target_status`**: Comprobación de alcanzabilidad y latencia SSH de un target (`target`: string requerido).
4. **`list_directory`**: Listado seguro de directorios dentro de un proyecto (`target`, `project` requeridos, `relative_path` opcional).
5. **`file_stat`**: Metadatos de un archivo o carpeta dentro del root permitido (`target`, `project`, `relative_path` requeridos).
6. **`read_file`**: Lectura segura de archivos de texto con límite de 1 MiB (`target`, `project`, `relative_path` requeridos).
7. **`git_status`**: Ejecución de `git status --short` en la raíz del repositorio autorizado (`target`, `project` requeridos).
8. **`run_task`**: Ejecución de una tarea preconfigurada en la lista blanca (`target`, `project`, `task` requeridos).

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
- **Endpoint de Monitoreo**: `GET http://127.0.0.1:8090/health` retorna `{"status":"ok","adapter":"mcp-gateway-adapter","version":"0.2.0"}`.
- **Servicio del Sistema**: Administrado por systemd bajo el servicio `mcp-gateway-mcp.service` ejecutado como usuario `mcp-gateway` (UID 1001, sin sudo).

---

## 6. Puente con el Gateway Core (`mcp_gateway.bridge`)

El adaptador invoca al núcleo Python mediante subprocesos aislados:
```bash
python3 -m mcp_gateway.bridge invoke <tool_name> '<json_arguments>'
```
- Cada invocación inicia un proceso Python ligero que carga la configuración o registro SQLite actual.
- El tiempo de ejecución del puente en MCP-Pi es de aproximadamente **2.0 segundos**, atribuible a la inicialización del intérprete de Python 3.9 sobre la CPU single-core de 700 MHz.
- Respeta de inmediato el **Emergency Kill Switch**: si `gateway_enabled` es falso en la base de datos SQLite, la llamada es rechazada con el código estandarizado `GATEWAY_DISABLED`.

---

## 7. Identidad del Cliente y Limitaciones Actuales

- **Perfil Lógico**: En la Fase 4C, las peticiones del adaptador operan bajo el perfil lógico `mcp-local`.
- **Identidad Upstream No Autenticada**: El protocolo MCP recibe metadatos `clientInfo` autodeclarados por el cliente, los cuales no son criptográficamente confiables.
- **Fase 6 (External AI Clients)**: La asociación de identidades upstream autenticadas (OAuth de OpenAI, tokens de Gemini o Claude) con los registros de la tabla `ai_clients` se implementará formalmente en la Fase 6.

---

## 8. Integración con ChatGPT

> [!WARNING]
> **Aislamiento de Red y ChatGPT**:
> ChatGPT no puede conectarse de manera directa a un servidor MCP confinado exclusivamente a `127.0.0.1:8090`. Por principios de diseño y seguridad, el puerto 8090 **NO** debe exponerse a la LAN ni a Internet mediante port forwarding o servicios no auditados.
> La integración con ChatGPT en la Fase 6 requerirá la evaluación de un túnel seguro dedicado (Secure MCP Tunnel o conector de red privada).
