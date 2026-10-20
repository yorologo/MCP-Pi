/**
 * MCP-Pi - Official Website JavaScript
 * Vanilla JS, zero runtime frameworks, accessible theme & bilingual language switcher.
 * Storage keys: 'mcp_pi_theme', 'mcp_pi_language'
 */

(() => {
  const root = document.documentElement;

  const metadata = {
    es: {
      title: "MCP-Pi — Permisos claros. Acciones trazables.",
      description: "Gateway de seguridad Go-only para Model Context Protocol en Raspberry Pi y Linux. Delimita accesos, evalúa políticas deterministas y audita operaciones de agentes IA.",
      socialDescription: "Conecta herramientas de IA con permisos delimitados y actividad en SQLite.",
      twitterDescription: "Gateway de seguridad Go-only para Model Context Protocol. Permisos claros. Acciones trazables.",
      imageAlt: "Isotipo y arquitectura de seguridad de MCP-Pi",
      locale: "es_ES"
    },
    en: {
      title: "MCP-Pi — Clear permissions. Traceable actions.",
      description: "Go-only security gateway for Model Context Protocol on Raspberry Pi and Linux. Enforce deterministic policy, scoped privileges, and operation activity for AI agents.",
      socialDescription: "Connect AI tools through scoped permissions and SQLite activity.",
      twitterDescription: "Go-only security gateway for Model Context Protocol. Clear permissions. Traceable actions.",
      imageAlt: "MCP-Pi security architecture and brand isotipo",
      locale: "en_US"
    }
  };

  const messages = {
    "brand-by": {es: "Por Yorologo", en: "By Yorologo"},
    "demo-input-note": {es: "Caso interno de política; no es una petición MCP completa.", en: "Internal policy case; not a complete MCP request."},
    "details-technical": { es: "Explorar arquitectura, capacidades y límites", en: "Explore architecture, capabilities and limits" },
    "footer-portfolio": {"es": "Portafolio Yorologo", "en": "Yorologo portfolio"},
    "guide-client": {"es": "Configurar clientes y permisos", "en": "Configure clients and permissions"},
    "guide-check": {"es": "Consulta requisitos y versiones publicadas", "en": "Check requirements and published versions"},
    "demo-noscript": {"es": "Activa JavaScript para seleccionar escenarios. Esta demo no ejecuta operaciones.", "en": "Enable JavaScript to select scenarios. This demo runs no operations."},
    "demo-copy-failed": {"es": "No se pudo copiar; selecciona el texto.", "en": "Could not copy; select the text."},
    "nav-repo": {"es": "Repositorio MCP-Pi en GitHub", "en": "MCP-Pi repository on GitHub"},
    "nav-mobile": {"es": "Navegación móvil", "en": "Mobile navigation"},
    "nav-primary": {"es": "Navegación principal", "en": "Primary navigation"},
    "demo-audit-note": {"es": "Ejemplo educativo; no representa filas ni hashes de SQLite.", "en": "Educational example; does not represent SQLite rows or hashes."},
    "demo-trace-note": {"es": "Explicación resumida del caso; no es una traza de ejecución.", "en": "Case summary; this is not an execution trace."},
    "demo-boundary": {"es": "La ejecución real añade comprobaciones del Core y del transporte.", "en": "Real execution adds Core and transport checks."},
    "demo-reason-label": {"es": "Explicación:", "en": "Explanation:"},
    "demo-code-label": {"es": "Código interno:", "en": "Internal code:"},
    "arch-execution": {"es": "Ejecución", "en": "Execution"},
    "arch-control": {"es": "Política y control", "en": "Policy and control"},
    "arch-input": {"es": "Entrada", "en": "Input"},
    "hero-audit-note": {"es": "Evidencia según la operación", "en": "Evidence depends on the operation"},
    "hero-arm-note": {"es": "Paquete publicado con checksum", "en": "Published bundle with checksum"},
    "hero-deny-note": {"es": "Permisos por cliente y proyecto", "en": "Permissions by client and project"},
    "hero-cloud-note": {"es": "Go Core y Registry locales", "en": "Local Go Core and Registry"},
    "imageAlt": {"es": "Isotipo de MCP-Pi", "en": "MCP-Pi symbol"},
    // Navigation
    "nav-skip": { es: "Saltar al contenido principal", en: "Skip to main content" },
    "nav-home": { es: "Inicio", en: "Home" },
    "nav-why": { es: "¿Por qué MCP-Pi?", en: "Why MCP-Pi?" },
    "nav-arch": { es: "Arquitectura", en: "Architecture" },
    "nav-capabilities": { es: "Capacidades", en: "Capabilities" },
    "nav-demo": { es: "Demo Interactiva", en: "Interactive Demo" },
    "nav-guide": { es: "Guía Rápida", en: "Quick Guide" },
    "nav-faq": { es: "Preguntas", en: "FAQ" },
    "nav-github": { es: "GitHub", en: "GitHub" },
    "nav-menu-toggle": { es: "Abrir menú de navegación", en: "Open navigation menu" },
    "nav-lang-toggle": { es: "Cambiar idioma (ES/EN)", en: "Switch language (ES/EN)" },
    "nav-theme-toggle": { es: "Cambiar tema de color", en: "Switch color theme" },

    // Hero
    "hero-badge": {"es": "Acceso controlado para herramientas de IA", "en": "Controlled access for AI tools"},
    "hero-slogan": {"es": "Tu IA. Tus sistemas. Tus reglas.", "en": "Your AI. Your systems. Your rules."},
    "hero-title": {"es": "Permisos claros. Acciones trazables.", "en": "Clear permissions. Traceable actions."},
    "hero-desc": {"es": "MCP-Pi conecta tu asistente de IA con tus equipos. Comprueba permisos antes de leer archivos o ejecutar tareas autorizadas y registra actividad en SQLite. Tú decides el acceso; MCP-Pi no ejecuta el modelo de IA.", "en": "MCP-Pi connects your AI assistant to your systems. It checks permissions before reading files or running authorized tasks and records activity in SQLite. You decide access; MCP-Pi does not run the AI model."},
    "hero-cta-demo": {"es": "Probar demo", "en": "Try the demo"},
    "hero-cta-guide": {"es": "Empezar", "en": "Get started"},
    "hero-cta-github": { es: "Ver en GitHub", en: "View on GitHub" },
    "hero-pill-cloud": {"es": "Núcleo autohospedado", "en": "Self-hosted core"},
    "hero-pill-deny": { es: "Denegación por Defecto", en: "Deny by Default" },
    "hero-pill-arm": {"es": "Release Linux ARMv6", "en": "Linux ARMv6 release"},
    "hero-pill-audit": {"es": "Actividad en SQLite", "en": "SQLite activity"},

    // Why MCP-Pi
    "why-badge": { es: "Control y Seguridad", en: "Control & Security" },
    "why-title": { es: "¿Por qué existe MCP-Pi?", en: "Why does MCP-Pi exist?" },
    "why-subtitle": {"es": "Delegar tareas útiles sin conceder acceso general a tus sistemas.", "en": "Delegate useful work without granting broad access to your systems."},
    "why-problem-title": { es: "El riesgo: Dar acceso directo a la IA", en: "The Risk: Giving Direct Access to AI" },
    "why-problem-desc": {
      es: "Conectar un asistente de IA (Claude Desktop, Cursor, agentes autónomos) directamente a tus servidores mediante SSH o shell libre es peligroso: un desliz de prompt injection o una alucinación puede alterar bases de datos, ejecutar comandos destructivos o extraer secretos sin supervisión.",
      en: "Connecting an AI assistant directly to your servers via SSH or an open shell is dangerous: a prompt injection exploit or hallucination can wipe databases, run destructive commands, or leak private secrets without oversight."
    },
    "why-solution-title": {"es": "La solución: permisos antes de actuar", "en": "The solution: permission before action"},
    "why-solution-desc": {"es": "Un gateway en Go vincula la identidad del cliente y comprueba sus permisos antes de ejecutar una operación. Un Raspberry Pi puede alojarlo, pero el equipo separado no sustituye la configuración segura ni protege frente a todas las acciones de una herramienta autorizada.", "en": "A Go gateway binds the client identity and checks permissions before executing an operation. A Raspberry Pi can host it, but separate hardware does not replace secure configuration or protect against every action an authorized tool can perform."},
    "why-card1-title": { es: "Menor Privilegio Real", en: "Real Least Privilege" },
    "why-card1-desc": {"es": "La identidad vinculada determina el catálogo visible y los permisos aplicables al invocar una herramienta.", "en": "The bound identity determines the visible catalog and applicable invocation permissions."},
    "why-card2-title": { es: "Transporte SSH Anclado", en: "Pinned SSH Transport" },
    "why-card2-desc": {"es": "SSH comprueba la clave fijada del destino. Una clave distinta no se acepta automáticamente.", "en": "SSH checks the pinned target host key. A different key is never accepted automatically."},
    "why-card3-title": { es: "Kill Switches Globales", en: "Global Kill Switches" },
    "why-card3-desc": {"es": "Deshabilita nuevas escrituras o solicitudes de shell; no equivale a cancelar un comando ya iniciado.", "en": "Disable new write or shell requests; this does not cancel a command already running."},

    // Architecture
    "arch-badge": { es: "Diseño Robusto", en: "Robust Design" },
    "arch-title": { es: "Arquitectura y Flujo de Peticiones", en: "Architecture & Request Flow" },
    "arch-subtitle": { es: "Un único núcleo en Go que comparte transporte MCP, registry SQLite y motor de políticas.", en: "A single Go core sharing MCP transports, SQLite registry, and policy evaluation." },
    "arch-diag-client": { es: "Cliente IA (MCP)", en: "AI Client (MCP)" },
    "arch-diag-client-sub": {"es": "stdio / Streamable HTTP", "en": "stdio / Streamable HTTP"},
    "arch-diag-gateway": { es: "MCP-Pi Gateway (Go Core)", en: "MCP-Pi Gateway (Go Core)" },
    "arch-diag-gateway-sub": { es: "Policy Engine · Registry SQLite · Admin", en: "Policy Engine · Registry SQLite · Admin" },
    "arch-diag-target": { es: "Targets Remotos", en: "Remote Targets" },
    "arch-diag-target-sub": { es: "Linux SSH / Windows PowerShell", en: "Linux SSH / Windows PowerShell" },
    "arch-diag-audit": { es: "Auditoría SQLite", en: "SQLite Audit Trail" },
    "arch-diag-audit-sub": {"es": "Actividad persistida según la operación; sin garantía de inviolabilidad.", "en": "Activity persisted according to the operation; no tamper-proof guarantee."},
    "arch-step1-title": { es: "1. Vinculación de Identidad", en: "1. Identity Binding" },
    "arch-step1-desc": { es: "El cliente autentica su identidad al conectarse; no puede suplantar otro cliente en los argumentos.", en: "Client authenticates identity upon connection; cannot impersonate another client in arguments." },
    "arch-step2-title": { es: "2. Proyección de Catálogo", en: "2. Catalog Projection" },
    "arch-step2-desc": {"es": "tools/list filtra por grants. Ver una herramienta no garantiza que una invocación concreta esté permitida.", "en": "tools/list filters by grants. Seeing a tool does not guarantee permission for a particular invocation."},
    "arch-step3-title": { es: "3. Evaluación de Política", en: "3. Policy Evaluation" },
    "arch-step3-desc": {"es": "Comprueba grants, alcance y switches. El Core añade validaciones de ruta, ejecución y privilegios según la operación.", "en": "Checks grants, scope and switches. The Core adds path, execution and privilege checks for each operation."},
    "arch-step4-title": { es: "4. Ejecución Acotada", en: "4. Scoped Execution" },
    "arch-step4-desc": { es: "Transporte SSH seguro con clave de host anclada. Operaciones nativas en POSIX o PowerShell.", en: "Secure SSH transport with pinned host keys. Native POSIX or PowerShell commands." },
    "arch-step5-title": {"es": "5. Actividad y resultados", "en": "5. Activity and results"},
    "arch-step5-desc": {"es": "Las operaciones sensibles exigen evidencia durable. La persistencia de Activity no es un registro criptográfico de todas las peticiones.", "en": "Sensitive operations require durable evidence. Activity persistence is not a cryptographic log of every request."},

    // Capabilities & Limits
    "cap-badge": { es: "Transparencia Técnica", en: "Technical Transparency" },
    "cap-title": { es: "Capacidades y Límites Verificables", en: "Verifiable Capabilities & Limits" },
    "cap-subtitle": { es: "Claridad absoluta en lo que MCP-Pi hace y en lo que rehúsa hacer por diseño.", en: "Absolute clarity on what MCP-Pi does and what it intentionally refuses to do." },
    "cap-col1-title": {"es": "Capacidades implementadas", "en": "Implemented capabilities"},
    "cap-col2-title": { es: "Límites Estrictos de Seguridad", en: "Strict Security Boundaries" },
    "cap-feat1-title": {"es": "stdio y Streamable HTTP", "en": "stdio and Streamable HTTP"},
    "cap-feat1-desc": {"es": "La conexión depende del transporte, la identidad y la configuración del cliente. Consulta la guía de integración.", "en": "Connection depends on transport, identity and client configuration. See the integration guide."},
    "cap-feat2-title": { es: "Grants y Alcance por Proyecto", en: "Project-Scoped Grants" },
    "cap-feat2-desc": {"es": "Las operaciones estructuradas validan rutas y límites del proyecto. La shell de confianza no es un sandbox.", "en": "Structured operations validate paths and project boundaries. Trusted shell is not a sandbox."},
    "cap-feat3-title": { es: "Elevación target_admin Controlada", en: "Guarded target_admin Elevation" },
    "cap-feat3-desc": {"es": "Además del permiso ordinario: target_admin, política del destino, aprobación cuando proceda y backend nativo verificado.", "en": "In addition to ordinary permission: target_admin, target policy, any required approval and a verified native backend."},
    "cap-feat4-title": {"es": "Runtime Go en ARMv6", "en": "Go runtime on ARMv6"},
    "cap-feat4-desc": { es: "Construido en Go estático sin dependencias de Python ni Node en tiempo de ejecución.", en: "Built as a static Go binary with zero Python or Node runtime dependencies." },
    "cap-limit1-title": {"es": "Herramientas y grants explícitos", "en": "Explicit tools and grants"},
    "cap-limit1-desc": { es: "Cualquier herramienta fuera de catálogo o sin grant produce una denegación inmediata.", en: "Any tool outside catalog or missing grant results in an immediate fail-closed denial." },
    "cap-limit2-title": {"es": "Identidad SSH y errores explícitos", "en": "SSH identity and explicit errors"},
    "cap-limit2-desc": {"es": "Una clave distinta no cambia la confianza. Un timeout puede dejar incierto el resultado de una acción remota; requiere comprobación.", "en": "A different key does not change trust. A timeout may leave a remote action outcome uncertain; verification is required."},
    "cap-limit3-title": { es: "Sin Dependencia de Servicios Cloud", en: "No Cloud Dependency" },
    "cap-limit3-desc": {"es": "El núcleo puede operar sin nube. Los clientes de IA y las integraciones opcionales pueden depender de proveedores externos.", "en": "The core can operate without cloud services. AI clients and optional integrations may depend on external providers."},
    "cap-limit4-title": { es: "Sin Bypass de Sudo", en: "No Sudo Bypass" },
    "cap-limit4-desc": {"es": "La elevación usa mecanismos nativos verificados y no debilita las políticas sudo/UAC del destino.", "en": "Elevation uses verified native mechanisms and does not weaken target sudo/UAC policies."},

    // Use cases
    "use-badge": { es: "Casos Reales", en: "Real World" },
    "use-title": {"es": "Qué puedes hacer", "en": "What you can do"},
    "use-subtitle": { es: "Escenarios prácticos donde MCP-Pi protege tu entorno operativo.", en: "Practical scenarios where MCP-Pi protects your operating environment." },
    "use-case1-title": { es: "Asistente IA de Código con Acceso Acotado", en: "AI Coding Assistant with Scoped Access" },
    "use-case1-desc": {"es": "Concede lectura de archivos y consultas de Git en un proyecto, dejando escrituras y shell deshabilitadas. Los resultados que compartas con tu cliente de IA quedan sujetos a su configuración y proveedor.", "en": "Grant file reads and Git queries within a project, leaving writes and shell disabled. Results shared with your AI client are subject to its configuration and provider."},
    "use-case2-title": {"es": "Tareas autorizadas en tu laboratorio", "en": "Authorized tasks in your homelab"},
    "use-case2-desc": {"es": "Configura tareas permitidas para consultar estado o realizar mantenimiento. Revisa permisos y privilegios antes de habilitar operaciones sobre tus equipos.", "en": "Configure allowlisted tasks to inspect status or perform maintenance. Review permissions and privileges before enabling operations on your systems."},
    "use-case3-title": {"es": "Revisar actividad y diagnosticar", "en": "Review activity and diagnose"},
    "use-case3-desc": {"es": "Consulta Activity, estado y Doctor para investigar resultados y errores. Los registros no certifican cumplimiento ni resisten por sí solos a un administrador que pueda modificar SQLite.", "en": "Use Activity, status and Doctor to investigate results and errors. Logs do not certify compliance or independently resist an administrator who can modify SQLite."},

    // Demo Section
    "demo-badge": { es: "Laboratorio Interactivo", en: "Interactive Lab" },
    "demo-title": { es: "Simulador de Decisiones de Autorización", en: "Authorization Decision Simulator" },
    "demo-subtitle": {"es": "Nueve casos del contrato interno de autorización; resultados precomputados y comprobados con Go.", "en": "Nine internal authorization contract cases; precomputed results checked against Go."},
    "demo-sim-notice": {"es": "Laboratorio de Simulación Educativo: muestra decisiones internas de política, no respuestas MCP completas. No ejecuta comandos, no conecta con servidores y no escribe auditoría real. Aprobar una comprobación de privilegio no autoriza por sí solo una ejecución.", "en": "Educational Simulation Lab: shows internal policy decisions, not complete MCP responses. It runs no commands, connects to no servers and writes no real audit. Passing a privilege check does not by itself authorize execution."},
    "demo-select-label": { es: "Selecciona un escenario educativo:", en: "Select an educational scenario:" },
    "demo-req-heading": {"es": "1. Entrada del contrato interno", "en": "1. Internal contract input"},
    "demo-decision-heading": {"es": "2. Decisión interna de ejemplo", "en": "2. Example internal decision"},
    "demo-trace-heading": {"es": "3. Alcance de la comprobación", "en": "3. Scope of the check"},
    "demo-audit-heading": {"es": "4. Resumen simulado — no persistido", "en": "4. Simulated summary — not persisted"},
    "demo-copy-btn": { es: "Copiar", en: "Copy" },
    "demo-copied-btn": { es: "¡Copiado!", en: "Copied!" },
    "demo-verdict-allow": { es: "COMPROBACIÓN PERMITIDA", en: "CHECK ALLOWED" },
    "demo-verdict-deny": { es: "COMPROBACIÓN DENEGADA", en: "CHECK DENIED" },

    // Guide Section
    "guide-badge": { es: "Puesta en Marcha", en: "Getting Started" },
    "guide-title": { es: "Guía de Instalación y Uso Rápido", en: "Installation & Quickstart Guide" },
    "guide-subtitle": {"es": "Usa un paquete publicado compatible y completa el bootstrap antes de conectar clientes.", "en": "Use a compatible published bundle and complete bootstrap before connecting clients."},
    "guide-req-heading": { es: "Requisitos Verificados", en: "Verified Requirements" },
    "guide-req-1": {"es": "Paquete publicado de referencia: Linux ARMv6 (v1.6.11). Otras arquitecturas requieren un binario compatible o compilación desde código con Go.", "en": "Reference published bundle: Linux ARMv6 (v1.6.11). Other architectures require a compatible binary or building from source with Go."},
    "guide-req-2": {"es": "Host Linux con systemd y permisos administrativos. Revisa las precondiciones del instalador; no todas las distribuciones han sido validadas.", "en": "Linux host with systemd and administrative access. Review installer preconditions; not every distribution has been validated."},
    "guide-req-3": {"es": "Herramientas del sistema, curl, tar, sha256sum y cliente OpenSSH. El paquete incluye SQLite en Go; no requiere Python, Node ni compilador.", "en": "System tools, curl, tar, sha256sum and an OpenSSH client. The bundle includes SQLite in Go; no Python, Node or compiler is required."},
    "guide-step1-title": { es: "1. Descargar la release oficial", en: "1. Download official release" },
    "guide-step1-desc": {"es": "Ejemplo fijado a v1.6.11 / commit 5172705639d8. Descarga paquete y checksum; revisa GitHub Releases para otras versiones.", "en": "Example pinned to v1.6.11 / commit 5172705639d8. Download the bundle and checksum; see GitHub Releases for other versions."},
    "guide-step2-title": { es: "2. Verificar checksums SHA256", en: "2. Verify SHA256 checksums" },
    "guide-step2-desc": {"es": "Detente si falla el checksum. Solo después extrae el paquete y entra en su directorio. El checksum comprueba integridad; no es una firma independiente.", "en": "Stop if the checksum fails. Only then extract the bundle and enter its directory. The checksum checks integrity; it is not an independent signature."},
    "guide-step3-title": { es: "3. Ejecutar el instalador canónico", en: "3. Run canonical installer" },
    "guide-step3-desc": {"es": "En el directorio extraído, install.sh valida precondiciones y configura servicios, Registry y bootstrap de Admin. Lee el resultado antes de continuar.", "en": "In the extracted directory, install.sh validates preconditions and configures services, Registry and Admin bootstrap. Read its result before continuing."},
    "guide-step4-title": {"es": "4. Configurar Admin, permisos y cliente", "en": "4. Configure Admin, permissions and client"},
    "guide-step4-desc": {"es": "Admin escucha en loopback. Completa el bootstrap según el README, añade Target y clave SSH verificada, Project y grants del cliente. Usa Check Effective Access. ChatGPT y Gemini requieren sus integraciones y credenciales externas; no basta con copiar una ruta local.", "en": "Admin listens on loopback. Complete bootstrap using the README, add a Target with a verified SSH key, a Project and client grants. Use Check Effective Access. ChatGPT and Gemini require their integrations and external credentials; copying a local path is not sufficient."},

    // Canonical docs links
    "docs-heading": { es: "Documentación Canónica del Repositorio", en: "Canonical Repository Documentation" },
    "docs-sub": { es: "Consulta las guías técnicas completas mantenidas en el repositorio principal:", en: "Consult comprehensive technical manuals maintained in the core repository:" },
    "docs-link-arch": { es: "Arquitectura del Sistema (docs/architecture.md)", en: "System Architecture (docs/architecture.md)" },
    "docs-link-config": { es: "Guía de Configuración (docs/configuration.md)", en: "Configuration Guide (docs/configuration.md)" },
    "docs-link-ops": { es: "Operaciones y Mantenimiento (docs/operations.md)", en: "Operations & Maintenance (docs/operations.md)" },
    "docs-link-sec": { es: "Modelo de Seguridad (docs/security.md)", en: "Security Model (docs/security.md)" },
    "docs-link-rec": { es: "Recuperación y Respaldo (docs/recovery.md)", en: "Recovery & Backups (docs/recovery.md)" },
    "docs-link-contrib": { es: "Guía de Contribución (CONTRIBUTING.md)", en: "Contributing Guide (CONTRIBUTING.md)" },

    // FAQ
    "faq-badge": { es: "Resolución de Dudas", en: "Common Questions" },
    "faq-title": { es: "Preguntas Frecuentes", en: "Frequently Asked Questions" },
    "faq-subtitle": { es: "Respuestas directas a dudas comunes de arquitectura, seguridad y despliegue.", en: "Direct answers to architectural, security, and deployment questions." },
    "faq-q1": { es: "¿Por qué alojar el gateway en un Raspberry Pi independiente?", en: "Why host the gateway on an independent Raspberry Pi?" },
    "faq-a1": {"es": "Un equipo dedicado separa el despliegue del gateway de tus destinos. La protección depende de permisos, claves y administración segura; no garantiza aislamiento frente a toda operación autorizada ni frente a un host comprometido.", "en": "A dedicated machine separates gateway deployment from targets. Protection depends on permissions, keys and secure administration; it does not guarantee isolation from every authorized operation or a compromised host."},
    "faq-q2": { es: "¿Cómo se previenen ataques de inyección de comandos?", en: "How does MCP-Pi prevent command injection attacks?" },
    "faq-a2": {"es": "Prefiere herramientas estructuradas o tareas permitidas. run_command requiere autorización y shell habilitada, pero ejecuta shell de confianza: el proyecto determina alcance de autorización y directorio inicial, no un sandbox de archivos.", "en": "Prefer structured tools or allowlisted tasks. run_command requires authorization and enabled shell, but executes trusted shell: the project determines authorization scope and initial directory, not a filesystem sandbox."},
    "faq-q3": { es: "¿Qué ocurre si cambia la clave de host SSH del servidor remoto?", en: "What happens if a remote host's SSH key changes?" },
    "faq-a3": {"es": "Se rechaza una clave diferente sin sustituir la confianza. El transporte puede buscar la misma identidad fijada en otra dirección y reintentar de forma acotada; no acepta automáticamente una clave nueva.", "en": "A different key is rejected without replacing trust. The transport may locate the same pinned identity at another address and retry within bounds; it never automatically accepts a new key."},
    "faq-q4": { es: "¿Requiere conexión a internet o servicios externos?", en: "Does it require internet access or external cloud services?" },
    "faq-a4": {"es": "El Core y el Registry pueden operar en una red privada. Descargar paquetes y usar clientes de IA, el túnel OpenAI o Cloudflare puede requerir Internet y servicios externos. Revisa qué datos recibe tu cliente.", "en": "Core and Registry can operate on a private network. Downloading bundles and using AI clients, the OpenAI tunnel or Cloudflare may require Internet and external services. Review what data your client receives."},
    "faq-q5": { es: "¿Cómo se auditan las operaciones?", en: "How are operations audited?" },
    "faq-a5": {"es": "SQLite conserva Activity de las operaciones que la registran. Las mutaciones sensibles requieren evidencia durable y no deben informar éxito si falla la auditoría requerida. No existe una garantía universal de registrar toda petición ni un registro resistente a modificaciones.", "en": "SQLite stores Activity for operations that record it. Sensitive mutations require durable evidence and must not report success when required auditing fails. There is no universal guarantee that every request is recorded or that logs are tamper-proof."},

    // Footer
    "footer-tagline": { es: "Gateway de seguridad Go-only para Model Context Protocol.", en: "Go-only security gateway for Model Context Protocol." },
    "footer-ecosystem": { es: "Ecosistema Yorologo", en: "Yorologo Ecosystem" },
    "footer-resources": { es: "Recursos Técnicos", en: "Technical Resources" },
    "footer-repo": { es: "Repositorio MCP-Pi", en: "MCP-Pi Repository" },
    "footer-releases": { es: "Versiones y Releases", en: "Releases & Packages" },
    "footer-issues": { es: "Reporte de Problemas", en: "Issue Tracker" },
    "footer-rights": {"es": "Código y contribuciones en GitHub · Sin licencia de proyecto declarada", "en": "Code and contributions on GitHub · No project license declared"}
  };

  function getPreference(key) {
    try {
      return localStorage.getItem(key);
    } catch {
      return null;
    }
  }

  function savePreference(key, value) {
    try {
      localStorage.setItem(key, value);
    } catch {
      // Functional fallback if storage is blocked
    }
  }

  function setMeta(selector, value) {
    const el = document.querySelector(selector);
    if (el) el.setAttribute("content", value);
  }

  // Theme setup: support 'mcp_pi_theme' or prefers-color-scheme
  const savedTheme = getPreference("mcp_pi_theme");
  const prefersDark = window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches;
  let currentTheme = ["light", "dark"].includes(savedTheme) ? savedTheme : "system";
  const isDarkInitial = currentTheme === "system" ? prefersDark : currentTheme === "dark";
  root.classList.toggle("dark", isDarkInitial);

  // Language setup: support 'mcp_pi_language' or navigator.language
  const savedLang = getPreference("mcp_pi_language");
  const browserLang = (navigator.language || "en").toLowerCase();
  let currentLanguage = (savedLang === "es" || savedLang === "en")
    ? savedLang
    : browserLang.startsWith("es") ? "es" : "en";
  root.lang = currentLanguage;

  function translateContent(lang) {
    for (const el of document.querySelectorAll("[data-i18n]")) {
      const key = el.getAttribute("data-i18n");
      const entry = messages[key];
      if (entry && entry[lang]) {
        el.textContent = entry[lang];
      }
    }
    for (const el of document.querySelectorAll("[data-i18n-aria]")) {
      const key = el.getAttribute("data-i18n-aria");
      const entry = messages[key];
      if (entry && entry[lang]) {
        el.setAttribute("aria-label", entry[lang]);
      }
    }
    for (const el of document.querySelectorAll("[data-i18n-alt]")) {
      const key = el.getAttribute("data-i18n-alt");
      const entry = messages[key];
      if (entry && entry[lang]) {
        el.setAttribute("alt", entry[lang]);
      }
    }
  }

  document.addEventListener("DOMContentLoaded", () => {
    const themeToggle = document.getElementById("themeToggle");
    const themeIcon = document.getElementById("themeIcon");
    const languageToggle = document.getElementById("languageToggle");
    const languageLabel = document.getElementById("languageLabel");
    const mobileMenuBtn = document.getElementById("mobileMenuBtn");
    const mobileMenu = document.getElementById("mobileMenu");
    const scenarioSelector = document.getElementById("scenarioSelector");
    const currentYear = document.getElementById("currentYear");

    if (currentYear) {
      currentYear.textContent = String(new Date().getFullYear());
    }

    function renderTheme(isDark) {
      root.classList.toggle("dark", isDark);
      if (themeIcon) {
        themeIcon.innerHTML = isDark
          ? '<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 3v1m0 16v1m9-9h-1M4 12H3m15.364 6.364l-.707-.707M6.343 6.343l-.707-.707m12.728 0l-.707.707M6.343 17.657l-.707.707M16 12a4 4 0 11-8 0 4 4 0 018 0z" />'
          : '<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M20.354 15.354A9 9 0 018.646 3.646 9.003 9.003 0 0012 21a9.003 9.003 0 008.354-5.646z" />';
      }
      if (themeToggle) {
        const next = { system: "light", light: "dark", dark: "system" }[currentTheme];
        const names = currentLanguage === "es"
          ? { system: "sistema", light: "claro", dark: "oscuro" }
          : { system: "system", light: "light", dark: "dark" };
        const label = currentLanguage === "es"
          ? `Tema: ${names[currentTheme]}. Cambiar a ${names[next]}`
          : `Theme: ${names[currentTheme]}. Switch to ${names[next]}`;
        themeToggle.setAttribute("aria-label", label);
        themeToggle.setAttribute("title", label);
        const themeLabel = document.getElementById("themeLabel");
        if (themeLabel) themeLabel.textContent = names[currentTheme];
      }
    }

    function renderLanguage(lang) {
      currentLanguage = lang;
      root.lang = lang;
      translateContent(lang);

      const meta = metadata[lang] || metadata.en;
      const page = root.getAttribute("data-page");
      document.title = page === "demo" ? messages["demo-title"][lang] + " | MCP-Pi"
        : page === "guide" ? messages["guide-title"][lang] + " | MCP-Pi" : meta.title;
      setMeta('meta[name="description"]', meta.description);
      setMeta('meta[property="og:title"]', document.title);
      setMeta('meta[property="og:description"]', meta.socialDescription);
      setMeta('meta[property="og:locale"]', meta.locale);
      setMeta('meta[property="og:image:alt"]', meta.imageAlt);
      setMeta('meta[name="twitter:title"]', document.title);
      setMeta('meta[name="twitter:description"]', meta.twitterDescription);

      if (languageLabel) {
        languageLabel.textContent = lang === "es" ? "EN" : "ES";
      }
      if (languageToggle) {
        languageToggle.setAttribute(
          "aria-label",
          lang === "es" ? "Cambiar a inglés" : "Switch to Spanish"
        );
      }

      renderTheme(root.classList.contains("dark"));
      renderScenario();
    }

    // Display precomputed internal decisions; no policy evaluation in the browser.
    const scenarios = (typeof DEMO_SCENARIOS !== "undefined") ? DEMO_SCENARIOS : [];

    function populateScenarios() {
      if (!scenarioSelector || scenarios.length === 0) return;
      scenarioSelector.innerHTML = "";
      scenarioSelector.disabled = false;
      scenarios.forEach((sc, idx) => {
        const opt = document.createElement("option");
        opt.value = sc.id;
        opt.textContent = `${idx + 1}. [${sc.decision.allowed ? "ALLOW" : "DENY"}] ${currentLanguage === "es" ? sc.title_es : sc.title_en}`;
        scenarioSelector.appendChild(opt);
      });
    }

    function renderScenario() {
      if (!scenarioSelector || scenarios.length === 0) return;
      const selectedId = scenarioSelector.value || scenarios[0].id;
      const sc = scenarios.find((s) => s.id === selectedId) || scenarios[0];

      // Update selector options text in current language
      Array.from(scenarioSelector.options).forEach((opt) => {
        const item = scenarios.find((s) => s.id === opt.value);
        if (item) {
          const prefix = item.decision.allowed ? "ALLOW" : "DENY";
          opt.textContent = `[${prefix}] ${currentLanguage === "es" ? item.title_es : item.title_en}`;
        }
      });

      // Description & Context
      const descEl = document.getElementById("demoScenarioDesc");
      if (descEl) {
        descEl.textContent = currentLanguage === "es" ? sc.description_es : sc.description_en;
      }

      // Request Payload display
      const reqEl = document.getElementById("demoRequestJson");
      if (reqEl) {
        reqEl.textContent = JSON.stringify(sc.request, null, 2);
      }

      // Verdict Badge
      const verdictEl = document.getElementById("demoVerdictBadge");
      const codeEl = document.getElementById("demoDecisionCode");
      const reasonEl = document.getElementById("demoDecisionReason");

      if (verdictEl) {
        if (sc.decision.allowed) {
          verdictEl.className = "inline-flex items-center px-3 py-1 rounded-full text-sm font-semibold status-pill-allow";
          verdictEl.innerHTML = `<svg class="w-4 h-4 mr-1.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"/></svg> ${currentLanguage === "es" ? "COMPROBACIÓN PERMITIDA" : "CHECK ALLOWED"}`;
        } else {
          verdictEl.className = "inline-flex items-center px-3 py-1 rounded-full text-sm font-semibold status-pill-deny";
          verdictEl.innerHTML = `<svg class="w-4 h-4 mr-1.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"/></svg> ${currentLanguage === "es" ? "COMPROBACIÓN DENEGADA" : "CHECK DENIED"}`;
        }
      }

      if (codeEl) {
        codeEl.textContent = sc.decision.code || (currentLanguage === "es" ? "Sin código de error" : "No error code");
      }

      if (reasonEl) {
        reasonEl.textContent = currentLanguage === "es" ? sc.decision.reason_es : sc.decision.reason_en;
      }

      // Pipeline Trace
      const traceContainer = document.getElementById("demoTraceContainer");
      if (traceContainer) {
        traceContainer.innerHTML = "";
        sc.trace.forEach((step) => {
          const item = document.createElement("div");
          item.className = "flex items-start space-x-3 text-sm py-1.5 border-b border-slate-100 dark:border-slate-800 last:border-0";
          const note = currentLanguage === "es" ? step.note_es : step.note_en;
          item.textContent = note;
          traceContainer.appendChild(item);
        });
      }

      // Audit Payload display
      const auditEl = document.getElementById("demoAuditJson");
      if (auditEl) {
        auditEl.textContent = JSON.stringify(sc.audit_record, null, 2);
      }
    }

    // Copy to clipboard helper
    document.querySelectorAll("[data-copy-target]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const targetId = btn.getAttribute("data-copy-target");
        const targetEl = document.getElementById(targetId);
        if (!targetEl) return;
        const text = targetEl.textContent || "";
        try {
          await navigator.clipboard.writeText(text);
          btn.textContent = currentLanguage === "es" ? "¡Copiado!" : "Copied!";
        } catch {
          btn.textContent = messages["demo-copy-failed"][currentLanguage];
        }
      });
    });

    if (scenarioSelector) {
      populateScenarios();
      scenarioSelector.addEventListener("change", renderScenario);
    }

    if (languageToggle) {
      languageToggle.addEventListener("click", () => {
        const next = currentLanguage === "es" ? "en" : "es";
        savePreference("mcp_pi_language", next);
        renderLanguage(next);
      });
    }

    if (themeToggle) {
      themeToggle.addEventListener("click", () => {
        currentTheme = { system: "light", light: "dark", dark: "system" }[currentTheme];
        savePreference("mcp_pi_theme", currentTheme);
        const isDark = currentTheme === "system"
          ? window.matchMedia("(prefers-color-scheme: dark)").matches : currentTheme === "dark";
        renderTheme(isDark);
      });
    }

    if (mobileMenuBtn && mobileMenu) {
      mobileMenuBtn.addEventListener("click", () => {
        const isHidden = mobileMenu.classList.contains("hidden");
        mobileMenu.classList.toggle("hidden", !isHidden);
        mobileMenuBtn.setAttribute("aria-expanded", String(isHidden));
      });

      mobileMenu.querySelectorAll("a").forEach((link) => {
        link.addEventListener("click", () => {
          mobileMenu.classList.add("hidden");
          mobileMenuBtn.setAttribute("aria-expanded", "false");
        });
      });
      mobileMenu.addEventListener("keydown", (event) => {
        if (event.key === "Escape") {
          mobileMenu.classList.add("hidden");
          mobileMenuBtn.setAttribute("aria-expanded", "false");
          mobileMenuBtn.focus();
        }
      });
    }

    // System theme change listener (when no explicit override is stored)
    if (window.matchMedia) {
      window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", (e) => {
        if (currentTheme === "system") {
          renderTheme(e.matches);
        }
      });
    }

    // Initial render
    renderLanguage(currentLanguage);
  });
})();
