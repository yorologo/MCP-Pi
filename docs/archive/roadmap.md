# Roadmap y limitaciones conocidas

El roadmap sigue KISS: prioriza estabilidad y operación real sobre nuevas capas.

## Estado actual

- Gateway ARMv6 operativo en MCP-Pi.
- 21 herramientas MCP deterministas con grants dinámicos.
- Secure MCP Tunnel hacia OpenAI.
- Target principal Android/Termux con resolución dinámica de endpoint.
- Admin Console responsive disponible en la LAN confiable.
- Backup, Doctor, maintenance, rollback y auditoría integrados.

## Prioridades razonables

1. **Regresión E2E del catálogo OpenAI**: mantener una comprobación reproducible que detecte si el catálogo proyectado por ChatGPT difiere de `tools/list` del gateway.
2. **Observabilidad KISS**: mejorar métricas y diagnóstico únicamente donde ayuden a resolver fallos reales; evitar stacks pesados en ARMv6.
3. **UX administrativa**: continuar mejoras pequeñas basadas en uso real, sin migrar a SPA ni introducir un framework frontend.
4. **Más Targets**: validar otros workers únicamente cuando exista una necesidad concreta y preservar el mismo modelo Target → Project → Grant.
5. **Release hygiene**: alinear manifest/checksums/documentación en cada release formal posterior.

## Limitaciones conocidas

- Raspberry Pi Model A+ tiene CPU/RAM muy limitados; builds y tareas pesadas pertenecen a los Targets.
- El Admin Console usa HTTP dentro de una LAN confiable; en redes no confiables se debe usar túnel SSH/VPN.
- Android/Termux no ofrece aislamiento POSIX independiente entre Projects; el gateway compensa con Project roots, path validation, grants y auditoría.
- `run_command` es deliberadamente una capacidad de alto riesgo y solo debe concederse a clientes de confianza.
- La IP de Targets Android puede cambiar; la identidad canónica es Target ID + SSH host key, no la IP.
- `apply_patch` no forma parte del catálogo actual; las ediciones usan filesystem tools o `run_command` cuando está autorizado.

## Fuera del roadmap inmediato

- Docker/Kubernetes en MCP-Pi.
- SPA/frontend framework pesado.
- Base de datos externa.
- Auto-update sin gates/rollback.
- Exposición pública directa del MCP o del Admin Console.
