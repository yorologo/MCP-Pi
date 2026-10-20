/**
 * MCP-Pi - Official Website JavaScript
 * Vanilla JS, zero runtime frameworks, accessible theme & bilingual language switcher.
 * Storage keys: 'mcp_pi_theme', 'mcp_pi_language'
 */

(() => {
  const root = document.documentElement;

  const metadata = {
    es: {
      title: "MCP-Pi — Tu IA. Tus sistemas. Tus reglas.",
      description: "Gateway de seguridad Go-only para Model Context Protocol en Raspberry Pi y Linux. Delimita accesos, evalúa políticas deterministas y audita operaciones de agentes IA.",
      socialDescription: "Conecta agentes de inteligencia artificial a tus sistemas con políticas estrictas, privilegios acotados y auditoría inmutable desde un Raspberry Pi.",
      twitterDescription: "Gateway de seguridad Go-only para Model Context Protocol. Tu IA. Tus sistemas. Tus reglas.",
      imageAlt: "Isotipo y arquitectura de seguridad de MCP-Pi",
      locale: "es_ES"
    },
    en: {
      title: "MCP-Pi — Your AI. Your systems. Your rules.",
      description: "Go-only security gateway for Model Context Protocol on Raspberry Pi and Linux. Enforce deterministic policy, scoped privileges, and full audit trails for AI agents.",
      socialDescription: "Connect AI agents to real infrastructure with strict policies, scoped capabilities, and immutable audit logs from a Raspberry Pi.",
      twitterDescription: "Go-only security gateway for Model Context Protocol. Your AI. Your systems. Your rules.",
      imageAlt: "MCP-Pi security architecture and brand isotipo",
      locale: "en_US"
    }
  };

  const messages = {
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
    "hero-badge": { es: "Gateway de Seguridad Go-Only · Appliance ARMv6 / Linux", en: "Go-Only Security Gateway · ARMv6 / Linux Appliance" },
    "hero-title": { es: "Tu IA. Tus sistemas. Tus reglas.", en: "Your AI. Your systems. Your rules." },
    "hero-desc": {
      es: "MCP-Pi es la barrera de seguridad física y lógica entre modelos de inteligencia artificial y tu infraestructura real. Ejecuta un único binario Go en Raspberry Pi que valida identidades, restringe herramientas y audita cada invocación sin comprometer claves SSH ni contraseñas privilegiadas.",
      en: "MCP-Pi is the physical and logical security boundary between AI models and your real infrastructure. Running as a single Go binary on Raspberry Pi, it verifies identities, restricts tools, and audits every invocation without exposing blanket SSH keys or privileged credentials."
    },
    "hero-cta-demo": { es: "Explorar Laboratorio Demo", en: "Explore Interactive Demo" },
    "hero-cta-guide": { es: "Guía de Instalación", en: "Installation Guide" },
    "hero-cta-github": { es: "Ver en GitHub", en: "View on GitHub" },
    "hero-pill-cloud": { es: "Cero Dependencias de Nube", en: "Zero Cloud Dependencies" },
    "hero-pill-deny": { es: "Denegación por Defecto", en: "Deny by Default" },
    "hero-pill-arm": { es: "Binario ARMv6 / x86_64", en: "ARMv6 / x86_64 Binary" },
    "hero-pill-audit": { es: "Auditoría Forense SQLite", en: "SQLite Forensic Audit" },

    // Why MCP-Pi
    "why-badge": { es: "Control y Seguridad", en: "Control & Security" },
    "why-title": { es: "¿Por qué existe MCP-Pi?", en: "Why does MCP-Pi exist?" },
    "why-subtitle": { es: "Cerrar la brecha de confianza entre agentes autónomos e infraestructura de producción.", en: "Closing the trust gap between autonomous agents and production infrastructure." },
    "why-problem-title": { es: "El riesgo: Dar acceso directo a la IA", en: "The Risk: Giving Direct Access to AI" },
    "why-problem-desc": {
      es: "Conectar un asistente de IA (Claude Desktop, Cursor, agentes autónomos) directamente a tus servidores mediante SSH o shell libre es peligroso: un desliz de prompt injection o una alucinación puede alterar bases de datos, ejecutar comandos destructivos o extraer secretos sin supervisión.",
      en: "Connecting an AI assistant directly to your servers via SSH or an open shell is dangerous: a prompt injection exploit or hallucination can wipe databases, run destructive commands, or leak private secrets without oversight."
    },
    "why-solution-title": { es: "La solución: Aislamiento físico y estricto", en: "The Solution: Hardware Isolation & Strict Policy" },
    "why-solution-desc": {
      es: "MCP-Pi se sitúa en medio como un appliance de hardware independiente (un Raspberry Pi de bajo consumo). La IA solo puede comunicarse con el gateway mediante el protocolo Model Context Protocol (MCP). Cada llamada es interceptada, verificada y auditada antes de tocar cualquier equipo de destino.",
      en: "MCP-Pi sits in between as an independent hardware appliance (a low-power Raspberry Pi). The AI only communicates with the gateway via Model Context Protocol (MCP). Every tool call is intercepted, checked, and recorded before touching target hosts."
    },
    "why-card1-title": { es: "Menor Privilegio Real", en: "Real Least Privilege" },
    "why-card1-desc": { es: "La IA solo ve y ejecuta las herramientas y proyectos autorizados para su ID específico.", en: "The AI only sees and invokes the exact tools and projects authorized for its specific client ID." },
    "why-card2-title": { es: "Transporte SSH Anclado", en: "Pinned SSH Transport" },
    "why-card2-desc": { es: "Huellas criptográficas de host verificadas. Se rechazan suplantaciones de servidores remotos.", en: "Cryptographic host-key fingerprints pinned. Man-in-the-middle impersonations fail closed." },
    "why-card3-title": { es: "Kill Switches Globales", en: "Global Kill Switches" },
    "why-card3-desc": { es: "Desactiva escrituras o ejecución de comandos instantáneamente sin apagar los servicios de consulta.", en: "Instantly toggle global writes or shell execution off without disabling read and health probes." },

    // Architecture
    "arch-badge": { es: "Diseño Robusto", en: "Robust Design" },
    "arch-title": { es: "Arquitectura y Flujo de Peticiones", en: "Architecture & Request Flow" },
    "arch-subtitle": { es: "Un único núcleo en Go que comparte transporte MCP, registry SQLite y motor de políticas.", en: "A single Go core sharing MCP transports, SQLite registry, and policy evaluation." },
    "arch-diag-client": { es: "Cliente IA (MCP)", en: "AI Client (MCP)" },
    "arch-diag-client-sub": { es: "Claude, Cursor, CLI, Stdio/SSE", en: "Claude, Cursor, CLI, Stdio/SSE" },
    "arch-diag-gateway": { es: "MCP-Pi Gateway (Go Core)", en: "MCP-Pi Gateway (Go Core)" },
    "arch-diag-gateway-sub": { es: "Policy Engine · Registry SQLite · Admin", en: "Policy Engine · Registry SQLite · Admin" },
    "arch-diag-target": { es: "Targets Remotos", en: "Remote Targets" },
    "arch-diag-target-sub": { es: "Linux SSH / Windows PowerShell", en: "Linux SSH / Windows PowerShell" },
    "arch-diag-audit": { es: "Auditoría SQLite", en: "SQLite Audit Trail" },
    "arch-diag-audit-sub": { es: "Evidencia forense inmutable", en: "Immutable forensic evidence" },
    "arch-step1-title": { es: "1. Vinculación de Identidad", en: "1. Identity Binding" },
    "arch-step1-desc": { es: "El cliente autentica su identidad al conectarse; no puede suplantar otro cliente en los argumentos.", en: "Client authenticates identity upon connection; cannot impersonate another client in arguments." },
    "arch-step2-title": { es: "2. Proyección de Catálogo", en: "2. Catalog Projection" },
    "arch-step2-desc": { es: "La lista de herramientas (tools/list) solo expone las capacidades asignadas al cliente conectado.", en: "The tool list (tools/list) only exposes capabilities explicitly assigned to the connected client." },
    "arch-step3-title": { es: "3. Evaluación de Política", en: "3. Policy Evaluation" },
    "arch-step3-desc": { es: "El Policy Engine valida grants, alcances por proyecto, límites de directorio y switches de emergencia.", en: "Policy Engine validates grants, project scopes, directory boundaries, and emergency switches." },
    "arch-step4-title": { es: "4. Ejecución Acotada", en: "4. Scoped Execution" },
    "arch-step4-desc": { es: "Transporte SSH seguro con clave de host anclada. Operaciones nativas en POSIX o PowerShell.", en: "Secure SSH transport with pinned host keys. Native POSIX or PowerShell commands." },
    "arch-step5-title": { es: "5. Auditoría Inmutable", en: "5. Immutable Audit" },
    "arch-step5-desc": { es: "Cada autorización, denegación y resultado se registra en SQLite con huella criptográfica.", en: "Every authorization, denial, and execution result is recorded in SQLite with cryptographic hashes." },

    // Capabilities & Limits
    "cap-badge": { es: "Transparencia Técnica", en: "Technical Transparency" },
    "cap-title": { es: "Capacidades y Límites Verificables", en: "Verifiable Capabilities & Limits" },
    "cap-subtitle": { es: "Claridad absoluta en lo que MCP-Pi hace y en lo que rehúsa hacer por diseño.", en: "Absolute clarity on what MCP-Pi does and what it intentionally refuses to do." },
    "cap-col1-title": { es: "Capacidades Garantizadas", en: "Guaranteed Capabilities" },
    "cap-col2-title": { es: "Límites Estrictos de Seguridad", en: "Strict Security Boundaries" },
    "cap-feat1-title": { es: "Transportes Stdio y HTTP SSE", en: "Stdio & HTTP SSE Transports" },
    "cap-feat1-desc": { es: "Soporte nativo para asistentes de escritorio locales y agentes remotos en red local.", en: "Native support for local desktop assistants and remote LAN agents." },
    "cap-feat2-title": { es: "Grants y Alcance por Proyecto", en: "Project-Scoped Grants" },
    "cap-feat2-desc": { es: "Restricción de operaciones de archivos al subárbol de proyecto; previene escape por symlinks.", en: "File operations restricted to project subtree; prevents symlink and reparse traversal." },
    "cap-feat3-title": { es: "Elevación target_admin Controlada", en: "Guarded target_admin Elevation" },
    "cap-feat3-desc": { es: "Requiere doble autorización explícita antes de ejecutar comandos con privilegios elevados.", en: "Requires explicit double authorization before running elevated privileged commands." },
    "cap-feat4-title": { es: "Eficiencia Extrema en ARMv6", en: "Extreme Efficiency on ARMv6" },
    "cap-feat4-desc": { es: "Construido en Go estático sin dependencias de Python ni Node en tiempo de ejecución.", en: "Built as a static Go binary with zero Python or Node runtime dependencies." },
    "cap-limit1-title": { es: "No Ejecuta Payloads Sin Autorizar", en: "Never Runs Unauthorized Payloads" },
    "cap-limit1-desc": { es: "Cualquier herramienta fuera de catálogo o sin grant produce una denegación inmediata.", en: "Any tool outside catalog or missing grant results in an immediate fail-closed denial." },
    "cap-limit2-title": { es: "No Oculta Errores ni Falla Abierto", en: "Never Fails Open" },
    "cap-limit2-desc": { es: "Discrepancias de clave de host, timeouts o caídas de red terminan en cierre seguro.", en: "Host-key mismatches, timeouts, or network loss terminate in safe rejection." },
    "cap-limit3-title": { es: "Sin Dependencia de Servicios Cloud", en: "No Cloud Dependency" },
    "cap-limit3-desc": { es: "100% autónomo y privado en tu red local. Tus datos e historial nunca salen de tu control.", en: "100% self-hosted and private on your LAN. Your data and history never leave your control." },
    "cap-limit4-title": { es: "Sin Bypass de Sudo", en: "No Sudo Bypass" },
    "cap-limit4-desc": { es: "El appliance no contiene reglas permisivas de sudoers ni credenciales maestras.", en: "The appliance does not contain wild sudoers rules or master root credentials." },

    // Use cases
    "use-badge": { es: "Casos Reales", en: "Real World" },
    "use-title": { es: "Casos de Uso Comprobados", en: "Proven Use Cases" },
    "use-subtitle": { es: "Escenarios prácticos donde MCP-Pi protege tu entorno operativo.", en: "Practical scenarios where MCP-Pi protects your operating environment." },
    "use-case1-title": { es: "Asistente IA de Código con Acceso Acotado", en: "AI Coding Assistant with Scoped Access" },
    "use-case1-desc": {
      es: "Permite que agentes como Claude o Cursor lean archivos de configuración y consulten estados git en un servidor de desarrollo, impidiendo categóricamente que editen archivos del sistema o reinicien servicios.",
      en: "Allows agents like Claude or Cursor to read configuration files and query git status on dev servers, while categorically preventing system file edits or service restarts."
    },
    "use-case2-title": { es: "Laboratorio Homelab y Domótica en Raspberry Pi", en: "Homelab & IoT Automation on Raspberry Pi" },
    "use-case2-desc": {
      es: "Convierte un Raspberry Pi Zero W o Pi 3 en un centinela de seguridad para orquestar microservicios, consultar métricas y ejecutar tareas programadas de forma totalmente privada.",
      en: "Turns a Raspberry Pi Zero W or Pi 3 into a physical security sentry to orchestrate microservices, inspect metrics, and execute scheduled tasks in total privacy."
    },
    "use-case3-title": { es: "Auditoría Forense y Cumplimiento", en: "Forensic Audit & Compliance" },
    "use-case3-desc": {
      es: "Registro transparente e incontrovertible de cada petición realizada por modelos de IA, con marcas de tiempo, cliente invocador, target, decisión y código de error para trazabilidad total.",
      en: "Transparent, incontrovertible log of every request made by AI models, with timestamps, caller client, target, decision, and error code for complete traceability."
    },

    // Demo Section
    "demo-badge": { es: "Laboratorio Interactivo", en: "Interactive Lab" },
    "demo-title": { es: "Simulador de Decisiones de Autorización", en: "Authorization Decision Simulator" },
    "demo-subtitle": { es: "Explora cómo evalúa el Policy Engine oficial de MCP-Pi ante diversas peticiones y permisos.", en: "Explore how MCP-Pi's official Policy Engine evaluates various requests and grants." },
    "demo-sim-notice": {
      es: "Laboratorio de Simulación Educativo: Los escenarios presentados corresponden a casos de prueba oficiales y reproducibles del motor de políticas de MCP-Pi. No ejecuta comandos reales ni interactúa con servidores privados.",
      en: "Educational Simulation Lab: Scenarios presented are derived from official, reproducible test cases of MCP-Pi's Policy Engine. It does not execute live commands or access private infrastructure."
    },
    "demo-select-label": { es: "Selecciona un escenario educativo:", en: "Select an educational scenario:" },
    "demo-req-heading": { es: "1. Petición del Cliente MCP", en: "1. MCP Client Request" },
    "demo-decision-heading": { es: "2. Veredicto del Policy Engine", en: "2. Policy Engine Verdict" },
    "demo-trace-heading": { es: "3. Pipeline de Verificación", en: "3. Verification Pipeline" },
    "demo-audit-heading": { es: "4. Registro de Auditoría Generado", en: "4. Generated Audit Record" },
    "demo-copy-btn": { es: "Copiar JSON", en: "Copy JSON" },
    "demo-copied-btn": { es: "¡Copiado!", en: "Copied!" },
    "demo-verdict-allow": { es: "AUTORIZADO", en: "AUTHORIZED" },
    "demo-verdict-deny": { es: "DENEGADO", en: "DENIED" },

    // Guide Section
    "guide-badge": { es: "Puesta en Marcha", en: "Getting Started" },
    "guide-title": { es: "Guía de Instalación y Uso Rápido", en: "Installation & Quickstart Guide" },
    "guide-subtitle": { es: "Instala y configura MCP-Pi en minutos directamente desde la última versión compatible.", en: "Install and configure MCP-Pi in minutes directly from the latest verified release." },
    "guide-req-heading": { es: "Requisitos Verificados", en: "Verified Requirements" },
    "guide-req-1": { es: "Hardware: Raspberry Pi (ARMv6, ARMv7, ARM64) o Linux x86_64", en: "Hardware: Raspberry Pi (ARMv6, ARMv7, ARM64) or Linux x86_64" },
    "guide-req-2": { es: "Sistema Operativo: Linux con systemd (Raspberry Pi OS, Debian 11/12, Ubuntu)", en: "Operating System: Linux with systemd (Raspberry Pi OS, Debian 11/12, Ubuntu)" },
    "guide-req-3": { es: "Software: OpenSSH client y SQLite 3.35+ (Cero dependencias de Python o Node)", en: "Software: OpenSSH client and SQLite 3.35+ (Zero Python or Node dependencies)" },
    "guide-step1-title": { es: "1. Descargar la release oficial", en: "1. Download official release" },
    "guide-step1-desc": { es: "Descarga el paquete inmutable empaquetado para tu arquitectura desde GitHub Releases.", en: "Download the immutable bundle packaged for your architecture from GitHub Releases." },
    "guide-step2-title": { es: "2. Verificar checksums SHA256", en: "2. Verify SHA256 checksums" },
    "guide-step2-desc": { es: "Comprueba la integridad criptográfica del paquete antes de la extracción.", en: "Verify cryptographic integrity of the release archive prior to extraction." },
    "guide-step3-title": { es: "3. Ejecutar el instalador canónico", en: "3. Run canonical installer" },
    "guide-step3-desc": { es: "El script install.sh configura los usuarios del sistema, servicios systemd y la base de datos inicial.", en: "The install.sh script sets up service accounts, systemd units, and initial schema." },
    "guide-step4-title": { es: "4. Conectar tu cliente MCP", en: "4. Connect your MCP client" },
    "guide-step4-desc": { es: "Añade la configuración en Claude Desktop (claude_desktop_config.json) o tu agente preferido:", en: "Add configuration to Claude Desktop (claude_desktop_config.json) or preferred agent:" },

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
    "faq-a1": {
      es: "El aislamiento físico garantiza que incluso si un agente de IA se descontrola o sufre un prompt injection severo, no tiene acceso a las claves maestras ni al hardware del servidor central. El Raspberry Pi actúa como una aduana insobornable.",
      en: "Hardware isolation guarantees that even if an AI agent malfunctions or is compromised via prompt injection, it cannot access master keys or central servers directly. The Raspberry Pi serves as an incorruptible gatekeeper."
    },
    "faq-q2": { es: "¿Cómo se previenen ataques de inyección de comandos?", en: "How does MCP-Pi prevent command injection attacks?" },
    "faq-a2": {
      es: "MCP-Pi no pasa comandos de texto a una shell abierta a menos que se use la herramienta 'run_command' con el switch 'shell_enabled=true' y un grant explícito. Las mutaciones de archivos usan primitivas atómicas del sistema de archivos con validación estricta de límites de proyecto.",
      en: "MCP-Pi does not pass arbitrary text to an open shell unless 'run_command' is invoked with 'shell_enabled=true' and an explicit grant. File mutations use atomic filesystem primitives with strict project boundary validation."
    },
    "faq-q3": { es: "¿Qué ocurre si cambia la clave de host SSH del servidor remoto?", en: "What happens if a remote host's SSH key changes?" },
    "faq-a3": {
      es: "MCP-Pi falla cerrado inmediatamente. Si la clave no coincide con el pin criptográfico registrado, la conexión se rechaza para evitar ataques Man-in-the-Middle, registrando una alerta en la auditoría.",
      en: "MCP-Pi fails closed immediately. If the key does not match the pinned cryptographic fingerprint, the connection is rejected to prevent Man-in-the-Middle attacks, recording an audit alert."
    },
    "faq-q4": { es: "¿Requiere conexión a internet o servicios externos?", en: "Does it require internet access or external cloud services?" },
    "faq-a4": {
      es: "No. MCP-Pi funciona de manera 100% local en tu red privada. No envía telemetría ni depende de APIs de terceros para evaluar políticas o gestionar el registry.",
      en: "No. MCP-Pi runs 100% locally on your private network. It transmits no telemetry and depends on zero third-party cloud APIs to evaluate policies or maintain registry state."
    },
    "faq-q5": { es: "¿Cómo se auditan las operaciones?", en: "How are operations audited?" },
    "faq-a5": {
      es: "Todas las peticiones recibidas, aprobadas y denegadas se escriben en la base de datos SQLite interna con marcas de tiempo ISO, identificadores de cliente, herramientas invocadas y códigos de resultado.",
      en: "All received, approved, and denied requests are persisted to an internal SQLite database with ISO timestamps, client IDs, invoked tools, and result codes."
    },

    // Footer
    "footer-tagline": { es: "Gateway de seguridad Go-only para Model Context Protocol.", en: "Go-only security gateway for Model Context Protocol." },
    "footer-ecosystem": { es: "Ecosistema Yorologo", en: "Yorologo Ecosystem" },
    "footer-resources": { es: "Recursos Técnicos", en: "Technical Resources" },
    "footer-repo": { es: "Repositorio MCP-Pi", en: "MCP-Pi Repository" },
    "footer-releases": { es: "Versiones y Releases", en: "Releases & Packages" },
    "footer-issues": { es: "Reporte de Problemas", en: "Issue Tracker" },
    "footer-rights": { es: "Proyecto de código abierto bajo licencia MIT · Ecosistema Yorologo", en: "Open source project under MIT License · Yorologo Ecosystem" }
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
  const isDarkInitial = savedTheme ? savedTheme === "dark" : prefersDark;
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
        const label = currentLanguage === "es"
          ? (isDark ? "Cambiar a modo claro" : "Cambiar a modo oscuro")
          : (isDark ? "Switch to light mode" : "Switch to dark mode");
        themeToggle.setAttribute("aria-label", label);
      }
    }

    function renderLanguage(lang) {
      currentLanguage = lang;
      root.lang = lang;
      translateContent(lang);

      const meta = metadata[lang] || metadata.en;
      document.title = meta.title;
      setMeta('meta[name="description"]', meta.description);
      setMeta('meta[property="og:title"]', meta.title);
      setMeta('meta[property="og:description"]', meta.socialDescription);
      setMeta('meta[property="og:locale"]', meta.locale);
      setMeta('meta[name="twitter:title"]', meta.title);
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

    // Interactive Demo Simulation Engine
    const scenarios = (typeof DEMO_SCENARIOS !== "undefined") ? DEMO_SCENARIOS : [];

    function populateScenarios() {
      if (!scenarioSelector || scenarios.length === 0) return;
      scenarioSelector.innerHTML = "";
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
          verdictEl.innerHTML = `<svg class="w-4 h-4 mr-1.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"/></svg> ${currentLanguage === "es" ? "AUTORIZADO" : "AUTHORIZED"}`;
        } else {
          verdictEl.className = "inline-flex items-center px-3 py-1 rounded-full text-sm font-semibold status-pill-deny";
          verdictEl.innerHTML = `<svg class="w-4 h-4 mr-1.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"/></svg> ${currentLanguage === "es" ? "DENEGADO" : "DENIED"}`;
        }
      }

      if (codeEl) {
        codeEl.textContent = sc.decision.code || "AUTHORIZED";
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
          const icon = step.passed
            ? `<span class="text-emerald-500 font-bold flex-shrink-0" aria-label="Passed">✔</span>`
            : `<span class="text-rose-500 font-bold flex-shrink-0" aria-label="Failed">✖</span>`;
          const note = currentLanguage === "es" ? step.note_es : step.note_en;
          item.innerHTML = `${icon} <span class="${step.passed ? 'text-slate-700 dark:text-slate-300' : 'text-rose-600 dark:text-rose-400 font-medium'}">${note}</span>`;
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
      btn.addEventListener("click", () => {
        const targetId = btn.getAttribute("data-copy-target");
        const targetEl = document.getElementById(targetId);
        if (!targetEl) return;
        const text = targetEl.textContent || "";
        navigator.clipboard.writeText(text).then(() => {
          const original = btn.textContent;
          btn.textContent = currentLanguage === "es" ? "¡Copiado!" : "Copied!";
          setTimeout(() => { btn.textContent = original; }, 2000);
        }).catch(() => {});
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
        const isDark = !root.classList.contains("dark");
        savePreference("mcp_pi_theme", isDark ? "dark" : "light");
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
    }

    // System theme change listener (when no explicit override is stored)
    if (window.matchMedia) {
      window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", (e) => {
        if (!getPreference("mcp_pi_theme")) {
          renderTheme(e.matches);
        }
      });
    }

    // Initial render
    renderLanguage(currentLanguage);
  });
})();