# MCP Gateway Lifecycle & Operations Guide

## 1. Visión General

La arquitectura de ciclo de vida de **MCP Gateway** proporciona operaciones deterministas para instalación, diagnóstico de salud, respaldo, restauración, actualización atómica y reversión (*rollback*), tanto mediante la interfaz de línea de comandos unificada (`bin/mcp-gateway`) como desde la consola web de administración (`/maintenance`).

---

## 2. Estructura de Directorios y Despliegue

```text
/home/mcp-gateway/
├── mcp-gateway/              # Instalación estándar o symlink 'current'
│   ├── bin/
│   │   ├── mcp-gateway        # Wrapper ejecutable CLI unificado
│   │   └── mcp-gateway-adapter# Binario Go compilado estáticamente (ARMv6)
│   ├── src/mcp_gateway/      # Código fuente Python stdlib
│   ├── config/               # Plantillas y unidades systemd
│   ├── tests/                # Suites de pruebas unitarias y de integración
│   ├── compatibility.json    # Matriz de compatibilidad y versiones
│   ├── manifest.json         # Manifiesto de release
│   ├── SHA256SUMS            # Sumas criptográficas de verificación
│   └── install.sh            # Script de instalación idempotente
├── .local/share/mcp-gateway/ # Directorio de datos (permisos 700)
│   ├── gateway.db            # Base de datos SQLite
│   └── backups/              # Respaldos automáticos de base de datos y archivos
└── .config/mcp-gateway/      # Directorio de configuración local (permisos 700)
```

---

## 3. CLI Unificado (`bin/mcp-gateway`)

El wrapper [`bin/mcp-gateway`](../../bin/mcp-gateway) es compatible con POSIX shell y no requiere dependencias externas:

| Comando | Descripción |
| :--- | :--- |
| `mcp-gateway status` | Consulta rápida del estado del Gateway, writes y número de targets. |
| `mcp-gateway doctor` | Ejecuta la batería completa de chequeos diagnósticos de salud. |
| `mcp-gateway repair` | Aplica correcciones no destructivas (permisos de directorios y recarga de servicios). |
| `mcp-gateway backup` | Genera un respaldo online y consistente de SQLite mediante la API nativa de backup. |
| `mcp-gateway restore <path>` | Valida integridad/esquema, migra v1/v2/v3→v4 cuando corresponde y limpia aprobaciones temporales de privilegio antes de devolver el Registry al servicio. |
| `mcp-gateway rollback` | Indica la ruta administrativa soportada para rollback; la reversión real del runtime requiere la operación root correspondiente. |
| `mcp-gateway uninstall [--purge]` | Detiene y desactiva servicios systemd; opcionalmente elimina los datos con `--purge`. |

---

## 4. Diagnóstico y Auto-Reparación Segura (`Doctor` & `Repair`)

El módulo [`doctor.py`](../../src/mcp_gateway/doctor.py) verifica:
1. **Runtime de Python**: Versión compatible (>= 3.9).
2. **Arquitectura de CPU**: Coincidencia con arquitecturas soportadas (`armv6l`, `aarch64`, `x86_64`).
3. **Contrato de Compatibilidad**: Validación íntegra contra `compatibility.json`.
4. **Integridad de SQLite**: Ejecución de `PRAGMA integrity_check` y `PRAGMA user_version`.
5. **Permisos de Archivos**: Permisos estrictos en directorios de datos (`700`) y claves SSH (`600`).
6. **Salud del Núcleo**: Inicialización de `GatewayTools` y estado de kill switch.
7. **Catálogo de Herramientas**: Presencia y orden determinista del catálogo vigente de 21 herramientas allowlisted.
8. **HTTP Probes**: Verificación en vivo de `/live`, `/ready`, protección de Host y protección de Origin.

El comando `repair` restringe permisos vulnerables y recarga los daemons sin alterar la configuración ni destruir datos.

---

## 5. Manifiesto de Release y Verificación de Integridad

Cada versión cuenta con un archivo [`manifest.json`](../../manifest.json) que certifica compatibilidad:
- Arquitectura objetivo
- Versión de API del Core y del Bridge
- Versión del catálogo de herramientas
- Protocolo MCP implementado

El archivo [`SHA256SUMS`](../../SHA256SUMS) garantiza que ningún archivo del paquete ha sido alterado de forma accidental o maliciosa.

---

## 6. Consola Web de Mantenimiento (`/maintenance`)

La interfaz administrativa incluye un panel de control con soporte de **HTMX local vendored** (cero dependencias externas o CDN):
- Visualización en tiempo real del estado de Doctor.
- Consulta de contratos de versiones y esquema de base de datos.
- Listado histórico de respaldos almacenados en el Gateway con tamaños y fecha.
- Acciones rápidas: *Re-run Diagnostics*, *Execute Safe Repair*, *Create Backup* y *Rollback*.
