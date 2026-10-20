/**
 * MCP-Pi - Verified Interactive Authorization Lab Scenarios
 * Derived from canonical Policy Engine contract tests (mcp-adapter/testdata/contracts/policy_cases.json)
 *
 * NOTE: Permanent simulation fixture. Demonstrates deterministic policy evaluation without executing live commands.
 */

const DEMO_SCENARIOS = [
  {
    id: "read_authorized",
    category: "allowed",
    title_es: "Lectura autorizada con grant de proyecto",
    title_en: "Authorized read with project grant",
    description_es: "El cliente autenticado cuenta con un grant activo con capacidad 'read' delimitado al proyecto 'core-api' en el target 'pi-node-01'. La operación se autoriza y audita.",
    description_en: "Authenticated client has an active grant with capability 'read' scoped to project 'core-api' on target 'pi-node-01'. The invocation is authorized and audited.",
    request: {
      client_id: "claude-dev-agent",
      tool: "read_file",
      target_id: "pi-node-01",
      project_id: "core-api",
      params: { path: "src/config/app.json" },
      privilege_requested: "standard"
    },
    trace: [
      { step: "identity_binding", passed: true, note_es: "Identidad verificada: claude-dev-agent (habilitado)", note_en: "Identity verified: claude-dev-agent (enabled)" },
      { step: "catalog_check", passed: true, note_es: "Herramienta 'read_file' válida en catálogo (requiere 'read' o '*')", note_en: "Tool 'read_file' recognized in catalog (requires 'read' or '*')" },
      { step: "grant_match", passed: true, note_es: "Grant coincidente: client=claude-dev-agent, cap=read, scope=pi-node-01/core-api", note_en: "Matching grant: client=claude-dev-agent, cap=read, scope=pi-node-01/core-api" },
      { step: "global_switches", passed: true, note_es: "gateway_enabled=true; no requiere writes_enabled ni shell_enabled", note_en: "gateway_enabled=true; does not require writes_enabled or shell_enabled" },
      { step: "boundary_check", passed: true, note_es: "Ruta dentro de la raíz de proyecto /opt/core-api (sin path traversal)", note_en: "Path inside project root /opt/core-api (no path traversal)" },
      { step: "privilege_check", passed: true, note_es: "Privilegio estándar no requiere elevación", note_en: "Standard privilege does not require elevation" }
    ],
    decision: {
      allowed: true,
      code: "AUTHORIZED",
      reason_es: "Operación de lectura autorizada conforme a la política y grant asignado.",
      reason_en: "Read operation authorized according to policy and active grant."
    },
    audit_record: {
      event_id: "aud_9a41b2c",
      timestamp: "2026-10-20T07:40:12Z",
      client_id: "claude-dev-agent",
      tool: "read_file",
      target_id: "pi-node-01",
      project_id: "core-api",
      decision: "ALLOW",
      error_code: null,
      sha256: "b6058d338de9dec9083389ff05f459c71ed2c4d87214b60f1ad892c5021a8f90"
    }
  },
  {
    id: "anonymous_denied",
    category: "denied_policy",
    title_es: "Denegación por cliente anónimo",
    title_en: "Anonymous client denied",
    description_es: "Se intenta invocar una herramienta sin autenticar la identidad del cliente (o pasando identidad 'anonymous' / 'none'). Se rechaza inmediatamente por denegación por defecto.",
    description_en: "Tool invocation attempted without authenticated client identity (or passing 'anonymous' / 'none'). Denied immediately by deny-by-default rule.",
    request: {
      client_id: "",
      tool: "health",
      target_id: null,
      project_id: null,
      params: {},
      privilege_requested: "standard"
    },
    trace: [
      { step: "identity_binding", passed: false, note_es: "Fallo: client_id vacío o anónimo. Se requiere identidad vinculada.", note_en: "Fail: client_id is empty or anonymous. Bound identity required." },
      { step: "catalog_check", passed: false, note_es: "Omitido tras fallo de identidad", note_en: "Skipped after identity failure" },
      { step: "grant_match", passed: false, note_es: "Omitido", note_en: "Skipped" },
      { step: "global_switches", passed: false, note_es: "Omitido", note_en: "Skipped" }
    ],
    decision: {
      allowed: false,
      code: "ANONYMOUS_CLIENT_DENIED",
      reason_es: "ANONYMOUS_CLIENT_DENIED: Se requiere identidad vinculada de cliente",
      reason_en: "ANONYMOUS_CLIENT_DENIED: Client identity required"
    },
    audit_record: {
      event_id: "aud_0f182c1",
      timestamp: "2026-10-20T07:41:03Z",
      client_id: "ANONYMOUS",
      tool: "health",
      target_id: null,
      project_id: null,
      decision: "DENY",
      error_code: "ANONYMOUS_CLIENT_DENIED",
      sha256: "4df3ffc50c1af3404e7532972ee3639379985ac81298c4109ba1982348dfca11"
    }
  },
  {
    id: "unknown_tool",
    category: "denied_policy",
    title_es: "Herramienta fuera de catálogo",
    title_en: "Tool not in gateway catalog",
    description_es: "El cliente solicita una herramienta no declarada en el catálogo oficial de MCP-Pi ('execute_raw_binary'). La solicitud se aborta antes de acceder al registry.",
    description_en: "Client requests a tool name not present in MCP-Pi's catalog ('execute_raw_binary'). Aborted before touching the registry.",
    request: {
      client_id: "claude-dev-agent",
      tool: "execute_raw_binary",
      target_id: "pi-node-01",
      project_id: "core-api",
      params: { binary: "/tmp/exploit" },
      privilege_requested: "standard"
    },
    trace: [
      { step: "identity_binding", passed: true, note_es: "Identidad verificada: claude-dev-agent", note_en: "Identity verified: claude-dev-agent" },
      { step: "catalog_check", passed: false, note_es: "Fallo: herramienta 'execute_raw_binary' inexistente en catálogo", note_en: "Fail: tool 'execute_raw_binary' not recognized in gateway catalog" },
      { step: "grant_match", passed: false, note_es: "Omitido", note_en: "Skipped" },
      { step: "global_switches", passed: false, note_es: "Omitido", note_en: "Skipped" }
    ],
    decision: {
      allowed: false,
      code: "TOOL_NOT_RECOGNIZED",
      reason_es: "TOOL_NOT_RECOGNIZED: La herramienta 'execute_raw_binary' no existe en el catálogo",
      reason_en: "TOOL_NOT_RECOGNIZED: Tool 'execute_raw_binary' not in gateway catalog"
    },
    audit_record: {
      event_id: "aud_33c14d2",
      timestamp: "2026-10-20T07:41:45Z",
      client_id: "claude-dev-agent",
      tool: "execute_raw_binary",
      target_id: "pi-node-01",
      project_id: "core-api",
      decision: "DENY",
      error_code: "TOOL_NOT_RECOGNIZED",
      sha256: "5172705639d843f2ba1aede680f0a45f3d1c0d170a831518f8e79b9a6291a260"
    }
  },
  {
    id: "missing_grant",
    category: "denied_policy",
    title_es: "Falta de grant para la herramienta",
    title_en: "Client lacks grant capability",
    description_es: "El cliente está registrado pero solo tiene capability 'read'. Intenta modificar un archivo con 'write_file', lo cual requiere la capability 'write'.",
    description_en: "Client is registered with capability 'read'. Attempts to invoke mutating tool 'write_file' which requires capability 'write'.",
    request: {
      client_id: "claude-dev-agent",
      tool: "write_file",
      target_id: "pi-node-01",
      project_id: "core-api",
      params: { path: "src/main.py", content: "# patch" },
      privilege_requested: "standard"
    },
    trace: [
      { step: "identity_binding", passed: true, note_es: "Identidad verificada: claude-dev-agent", note_en: "Identity verified: claude-dev-agent" },
      { step: "catalog_check", passed: true, note_es: "Herramienta 'write_file' requiere capability 'write' o '*'", note_en: "Tool 'write_file' requires capability 'write' or '*'" },
      { step: "grant_match", passed: false, note_es: "Fallo: el cliente posee 'read', carece de 'write'", note_en: "Fail: client has grant 'read', lacks capability 'write'" },
      { step: "global_switches", passed: false, note_es: "Omitido: denegación previa a evaluación de switches", note_en: "Skipped: grant check precedes switch evaluation" }
    ],
    decision: {
      allowed: false,
      code: "TOOL_NOT_ALLOWED",
      reason_es: "TOOL_NOT_ALLOWED: El cliente 'claude-dev-agent' carece de capability 'write' para 'write_file'",
      reason_en: "TOOL_NOT_ALLOWED: Client 'claude-dev-agent' lacks grant capability for tool 'write_file'"
    },
    audit_record: {
      event_id: "aud_49e21f9",
      timestamp: "2026-10-20T07:42:20Z",
      client_id: "claude-dev-agent",
      tool: "write_file",
      target_id: "pi-node-01",
      project_id: "core-api",
      decision: "DENY",
      error_code: "TOOL_NOT_ALLOWED",
      sha256: "141a5313d3d0f80dfa52a156efdd19d2e5aba1ff08a9f23cb6058d338de9dec9"
    }
  },
  {
    id: "writes_disabled_killswitch",
    category: "killswitch",
    title_es: "Kill switch global de escritura activo",
    title_en: "Global writes kill switch active",
    description_es: "El cliente posee el grant 'write', pero el administrador activó el interruptor de emergencia global writes_enabled=false en MCP-Pi.",
    description_en: "Client possesses 'write' grant, but administrator toggled the global emergency switch writes_enabled=false on MCP-Pi.",
    request: {
      client_id: "automation-bot",
      tool: "write_file",
      target_id: "pi-node-01",
      project_id: "core-api",
      params: { path: "logs/test.log", content: "data" },
      privilege_requested: "standard"
    },
    trace: [
      { step: "identity_binding", passed: true, note_es: "Identidad verificada: automation-bot", note_en: "Identity verified: automation-bot" },
      { step: "catalog_check", passed: true, note_es: "Herramienta 'write_file' válida", note_en: "Tool 'write_file' valid" },
      { step: "grant_match", passed: true, note_es: "Grant coincidente: capability 'write' presente", note_en: "Matching grant: capability 'write' present" },
      { step: "global_switches", passed: false, note_es: "Fallo: switch global writes_enabled está en 'false'", note_en: "Fail: global switch writes_enabled is set to 'false'" }
    ],
    decision: {
      allowed: false,
      code: "WRITES_DISABLED",
      reason_es: "WRITES_DISABLED: Las operaciones de escritura global están deshabilitadas",
      reason_en: "WRITES_DISABLED: Global writes are disabled"
    },
    audit_record: {
      event_id: "aud_71b99a0",
      timestamp: "2026-10-20T07:43:05Z",
      client_id: "automation-bot",
      tool: "write_file",
      target_id: "pi-node-01",
      project_id: "core-api",
      decision: "DENY",
      error_code: "WRITES_DISABLED",
      sha256: "9d1f1ee45a8b7c3d2e1f0a9b8c7d6e5f4a3b2c1d0e9f8a7b6c5d4e3f2a1b0c9d"
    }
  },
  {
    id: "target_shell_disabled",
    category: "killswitch",
    title_es: "Ejecución de shell denegada por switch",
    title_en: "Shell execution disabled by switch",
    description_es: "Intento de invocar 'run_command' para ejecutar un comando en el host remoto. Se deniega porque el switch estricto shell_enabled=false está activo en el gateway.",
    description_en: "Attempt to invoke 'run_command' to run shell commands on the remote target. Denied because strict switch shell_enabled=false is enforced.",
    request: {
      client_id: "claude-dev-agent",
      tool: "run_command",
      target_id: "pi-node-01",
      project_id: "core-api",
      params: { command: "uptime" },
      privilege_requested: "standard"
    },
    trace: [
      { step: "identity_binding", passed: true, note_es: "Identidad verificada: claude-dev-agent", note_en: "Identity verified: claude-dev-agent" },
      { step: "catalog_check", passed: true, note_es: "Herramienta 'run_command' en catálogo", note_en: "Tool 'run_command' in catalog" },
      { step: "grant_match", passed: true, note_es: "Grant de ejecución presente", note_en: "Execution grant present" },
      { step: "global_switches", passed: false, note_es: "Fallo: switch 'shell_enabled' está en false", note_en: "Fail: switch 'shell_enabled' is false" }
    ],
    decision: {
      allowed: false,
      code: "TARGET_SHELL_DISABLED",
      reason_es: "TARGET_SHELL_DISABLED: La ejecución de shell en targets de confianza está desactivada",
      reason_en: "TARGET_SHELL_DISABLED: Trusted target shell execution is disabled"
    },
    audit_record: {
      event_id: "aud_88fa12c",
      timestamp: "2026-10-20T07:43:50Z",
      client_id: "claude-dev-agent",
      tool: "run_command",
      target_id: "pi-node-01",
      project_id: "core-api",
      decision: "DENY",
      error_code: "TARGET_SHELL_DISABLED",
      sha256: "6f8a4361234567890abcdef1234567890abcdef1234567890abcdef123456789"
    }
  },
  {
    id: "privilege_denied_target_admin",
    category: "denied_privilege",
    title_es: "Elevación denegada sin grant target_admin",
    title_en: "Elevation denied without target_admin grant",
    description_es: "La solicitud requiere ejecución con privilegios elevados (sudo / root). El Policy Engine de MCP-Pi comprueba que el cliente carece de la capability 'target_admin'.",
    description_en: "Request demands elevated execution (sudo / root). MCP-Pi Policy Engine verifies client lacks the explicit 'target_admin' capability.",
    request: {
      client_id: "claude-dev-agent",
      tool: "run_command",
      target_id: "pi-node-01",
      project_id: "core-api",
      params: { command: "systemctl restart nginx" },
      privilege_requested: "elevated"
    },
    trace: [
      { step: "identity_binding", passed: true, note_es: "Identidad verificada: claude-dev-agent", note_en: "Identity verified: claude-dev-agent" },
      { step: "catalog_check", passed: true, note_es: "Herramienta 'run_command' válida", note_en: "Tool 'run_command' valid" },
      { step: "grant_match", passed: true, note_es: "Grant estándar de comandos presente", note_en: "Standard command grant present" },
      { step: "privilege_check", passed: false, note_es: "Fallo: se requiere grant explícito de capability 'target_admin' para elevar privilegios", note_en: "Fail: explicit 'target_admin' capability grant required for elevation" }
    ],
    decision: {
      allowed: false,
      code: "PRIVILEGE_GRANT_REQUIRED",
      reason_es: "PRIVILEGE_GRANT_REQUIRED: El cliente 'claude-dev-agent' requiere un grant explícito target_admin para pi-node-01",
      reason_en: "PRIVILEGE_GRANT_REQUIRED: Client 'claude-dev-agent' requires an explicit target_admin grant for pi-node-01"
    },
    audit_record: {
      event_id: "aud_92ff41b",
      timestamp: "2026-10-20T07:44:30Z",
      client_id: "claude-dev-agent",
      tool: "run_command",
      target_id: "pi-node-01",
      project_id: "core-api",
      decision: "DENY",
      error_code: "PRIVILEGE_GRANT_REQUIRED",
      sha256: "875d0541234567890abcdef1234567890abcdef1234567890abcdef123456789"
    }
  },
  {
    id: "privilege_authorized",
    category: "allowed",
    title_es: "Elevación autorizada con grant target_admin",
    title_en: "Elevation authorized with target_admin grant",
    description_es: "Un cliente administrativo con grant explícito 'target_admin' solicita una operación de mantenimiento. Tras doble verificación de host key y permisos, se aprueba.",
    description_en: "An administrative client with an explicit 'target_admin' grant requests a maintenance task. Double-verified through host key and privilege gates, then approved.",
    request: {
      client_id: "sysadmin-core",
      tool: "run_command",
      target_id: "pi-node-01",
      project_id: "system",
      params: { command: "systemctl status mcp-gateway.target" },
      privilege_requested: "elevated"
    },
    trace: [
      { step: "identity_binding", passed: true, note_es: "Identidad verificada: sysadmin-core (activo)", note_en: "Identity verified: sysadmin-core (active)" },
      { step: "catalog_check", passed: true, note_es: "Herramienta 'run_command' válida", note_en: "Tool 'run_command' valid" },
      { step: "grant_match", passed: true, note_es: "Grant coincidente presente", note_en: "Matching grant present" },
      { step: "global_switches", passed: true, note_es: "Switches globales en estado operativo", note_en: "Global switches in operational state" },
      { step: "privilege_check", passed: true, note_es: "Capability 'target_admin' verificada para pi-node-01/system", note_en: "Capability 'target_admin' verified for pi-node-01/system" }
    ],
    decision: {
      allowed: true,
      code: "AUTHORIZED_ELEVATED",
      reason_es: "Operación elevada autorizada con verificación de capability target_admin y pin SSH.",
      reason_en: "Elevated operation authorized with verified target_admin capability and SSH pin."
    },
    audit_record: {
      event_id: "aud_99c277a",
      timestamp: "2026-10-20T07:45:15Z",
      client_id: "sysadmin-core",
      tool: "run_command",
      target_id: "pi-node-01",
      project_id: "system",
      decision: "ALLOW",
      error_code: null,
      sha256: "0b0af3c1234567890abcdef1234567890abcdef1234567890abcdef123456789"
    }
  },
  {
    id: "health_killswitch_exception",
    category: "allowed",
    title_es: "Excepción vital: health con gateway desactivado",
    title_en: "Vital exception: health allowed during kill switch",
    description_es: "Incluso cuando el kill switch global gateway_enabled=false está activo, la herramienta 'health' permanece autorizada para permitir diagnóstico y monitoreo del estado.",
    description_en: "Even when global kill switch gateway_enabled=false is triggered, the 'health' probe remains authorized to enable diagnostics and status monitoring.",
    request: {
      client_id: "monitoring-daemon",
      tool: "health",
      target_id: null,
      project_id: null,
      params: {},
      privilege_requested: "standard"
    },
    trace: [
      { step: "identity_binding", passed: true, note_es: "Identidad verificada: monitoring-daemon", note_en: "Identity verified: monitoring-daemon" },
      { step: "catalog_check", passed: true, note_es: "Herramienta 'health' válida", note_en: "Tool 'health' valid" },
      { step: "grant_match", passed: true, note_es: "Grant con capability 'health' activo", note_en: "Grant with capability 'health' active" },
      { step: "global_switches", passed: true, note_es: "Excepción del motor: 'health' permitida con gateway_enabled=false", note_en: "Engine exception: 'health' permitted even when gateway_enabled=false" }
    ],
    decision: {
      allowed: true,
      code: "AUTHORIZED_DIAGNOSTIC",
      reason_es: "Sonda de salud autorizada como excepción de monitoreo y diagnóstico de seguridad.",
      reason_en: "Health probe authorized as safe diagnostic/monitoring exception."
    },
    audit_record: {
      event_id: "aud_01ea49d",
      timestamp: "2026-10-20T07:46:00Z",
      client_id: "monitoring-daemon",
      tool: "health",
      target_id: null,
      project_id: null,
      decision: "ALLOW",
      error_code: null,
      sha256: "1d4b98c80e5ee150071db3ee8b099845ba228dbc1234567890abcdef12345678"
    }
  }
];

if (typeof module !== "undefined" && module.exports) {
  module.exports = { DEMO_SCENARIOS };
} else if (typeof window !== "undefined") {
  window.DEMO_SCENARIOS = DEMO_SCENARIOS;
}