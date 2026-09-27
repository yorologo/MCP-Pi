# Local AI Clients Architecture & Integration

## 1. Visión General

El Gateway MCP-Pi implementa soporte nativo para **Clientes de Inteligencia Artificial Locales** (e.g. Gemini CLI, Claude Desktop, Cursor, etc.) sin exponer puertos de red a la LAN ni a Internet.

La comunicación se realiza mediante el transporte estándar **MCP Stdio** transmitido a través de túneles SSH autenticados con claves públicas/privadas dedicadas.

```text
+------------------------+
|    AI Client Local     |  (Antigravity / Gemini CLI / Claude Desktop)
|    (stdio transport)   |
+------------------------+
           |
           | SSH Stdio (Clave Ed25519 dedicada + Host Key Pinning)
           v
+------------------------+
|    sshd en MCP-Pi      |  (Puerto 22, LAN interna)
|   (forced-command)     |
+------------------------+
           |
           | Invoca /home/mcp-gateway/mcp-gateway/bin/mcp-gateway-client-stdio <client_id>
           v
+------------------------+
|  mcp-gateway-adapter   |  (Oficial Go SDK v1.7.0, MCP 2026-07-28)
|  - Stdio Transport     |
|  - SQLite Grants Check |
|  - Dynamic tools/list  |
|  - Guarded tools/call  |
+------------------------+
```

---

## 2. Modelo de Autenticación e Identidad Inmutable

Cada cliente cuenta con:
1. **Identidad Registrada (`ai_clients`)**:
   - `client_id`: Identificador canónico (e.g. `gemini-main`, `claude-desktop`).
   - `name`: Nombre descriptivo.
   - `enabled`: Booleano para activación/suspensión inmediata.
2. **Par de Claves Ed25519 Dedicado**:
   - Clave privada en el host del cliente (`~/.ssh/mcp_gemini_ed25519`).
   - Clave pública en MCP-Pi en `/home/mcp-gateway/.ssh/authorized_keys`.
3. **Forced Command Binding**:
   En el archivo `authorized_keys` de la cuenta `mcp-gateway`, cada clave está estrictamente prefijada:
   ```text
   command="/home/mcp-gateway/mcp-gateway/bin/mcp-gateway-client-stdio <client_id>",no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty ssh-ed25519 ...
   ```

### Garantías de Seguridad del Wrapper
- **Descarte de Comandos Arbitrarios**: La variable `SSH_ORIGINAL_COMMAND` se anula inmediatamente; el cliente no puede solicitar un shell bash ni ejecutar comandos arbitrarios en la Raspberry Pi.
- **Prevención de Suplantación**: El `client_id` es inyectado por la configuración de la clave en el servidor, no por el cliente. Un cliente con la clave de `claude-desktop` jamás puede asumir la identidad de `gemini-main`.
- **Mínimo Privilegio**: Todo el proceso corre bajo la cuenta de servicio `mcp-gateway` (UID 1001, sin sudo).

---

## 3. Catálogo Dinámico y Separación de Precedencia de Autorización
 
El adaptador MCP stdio asegura que `tools/list` y `tools/call` compartan la misma política estricta deny-by-default:
- **`tools/list`**: Evalúa en tiempo real `get_tools_catalog(client_id, for_catalog=True)`. Si el cliente carece de grant para una herramienta (por ejemplo, `write_file`), la herramienta se excluye de la respuesta al cliente.
- **`tools/call`**: Si el cliente intenta invocar una herramienta no autorizada por grants, el Policy Engine la rechaza inmediatamente como `TOOL_NOT_ALLOWED` / `unknown tool` sin evaluar ni revelar estados de interruptores operativos inferiores (`WRITES_DISABLED` o `project.write`). Si el cliente posee el grant pero el interruptor operativo está apagado, se rechaza de forma explícita con `WRITES_DISABLED`.
 
---
 
## 4. SSH Host Key Pinning Inmutable
 
Para evitar ataques Man-in-the-Middle y spoofing de red:
- La clave de host pública Ed25519 de MCP-Pi (`192.168.68.55`) se encuentra fijada en `~/.ssh/mcp_known_hosts`:
  ```text
  192.168.68.55 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOP3j98PiZxf8CKwfyEPCXbFSsV5bwfrI0704ZWR1plV
  ```
  Fingerprint SHA256: `SHA256:wovttruok3M1sdIkGHUs6pMbwKvTYylrh+Maz4Iv84E`.
- Todas las configuraciones generadas imponen:
  ```text
  StrictHostKeyChecking=yes
  UserKnownHostsFile=~/.ssh/mcp_known_hosts
  ```
- Comportamiento fail-closed verificado: ante discrepancia o clave adulterada, el proceso SSH aborta con código 255.
 
---
 
## 5. Estado de Clientes Locales Verificado
 
- **Gemini CLI**: `NOT_INSTALLED` (binario CLI `gemini` no instalado localmente). Cliente operativo real en esta sesión: `ANTIGRAVITY`. Archivo de configuración generado y listo en `~/.gemini/config/mcp_config.json`.
- **Claude Desktop**: `NOT_INSTALLED` (aplicación de escritorio `Claude` no instalada en Windows). Archivo de configuración generado y listo en `%APPDATA%\Claude\claude_desktop_config.json`.
- **Canal de Protocolo MCP**: Verificado al 100% sobre sesiones de protocolo reales vía SSH Stdio con las claves e identidades dedicadas `gemini-main` y `claude-desktop`.

---

## 6. Cliente Cloud OpenAI / ChatGPT (Secure MCP Tunnel)

- **Identidad Registrada**: `chatgpt-main`
- **Transporte**: Outbound-only Secure MCP Tunnel (`openai-tunnel-client` ARMv6) conectando a `http://127.0.0.1:8090/mcp`. Cero puertos entrantes expuestos.
- **Grants Asignados**:
  - `termux-main:MCP_Local` (capacidad `read`)
  - `*.*` (capacidad `admin` para herramientas de administración segura del appliance)
- **Herramientas de Administración Expuestas**:
  - `gateway_status`: Telemetría del appliance y métricas del sistema.
  - `gateway_doctor`: Chequeo de integridad en 19 puntos.
  - `gateway_backup`: Respaldos SQLite en caliente.
  - `gateway_maintenance`: Verificación y rotación de backups.
  - `gateway_reboot`: Reinicio controlado mediado por script con confirmación estricta.
- **Estado de Integración**: `LOCAL_SIDE_READY / OPENAI_PRODUCT_GATE_PENDING` (Ver `docs/chatgpt-gate.md`).

