# Client Grants & Granular Authorization Policy

MCP-Pi usa un único modelo de autorización **deny-by-default**. Los grants definen qué cliente puede usar una capacidad y en qué Target/Project; los kill switches, permisos de Project y la política de privilegios siguen siendo gates independientes.

## Esquema vigente

La tabla persistente es:

```sql
CREATE TABLE grants (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    client_id TEXT NOT NULL REFERENCES ai_clients(id),
    target_id TEXT NOT NULL REFERENCES targets(id),
    project_id TEXT,
    capability TEXT NOT NULL DEFAULT 'read',
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
```

Un grant puede usar una capacidad común (`read`, `write`, `execute`, `target_shell`, `target_admin`, `admin`) o capacidades específicas de herramientas. Se admiten valores separados por comas por compatibilidad. La UI recomienda grants separados cuando sea práctico.

El scope es:

```text
Client -> Target -> Project -> Capability -> Enabled
```

`* / * / *` existe para compatibilidad y acceso global deliberado. Tanto ese wildcard global como un `target_admin` global requieren confirmación explícita al crearse desde Admin Console.

## Autorización ordinaria

El Policy Engine `authorize_client()` es la fuente común para discovery y ejecución. Evalúa, en este orden práctico:

1. identidad y estado del cliente;
2. grant compatible con herramienta/Target/Project;
3. kill switch global;
4. kill switch de Target shell cuando corresponda;
5. Target y Project habilitados;
6. gates de escritura cuando la herramienta muta filesystem.

La UI **Check Effective Access** invoca este mismo motor; no existe un evaluador paralelo.

## Escrituras estructuradas

Las mutaciones `write_file`, `append_file`, `delete_file`, `copy_file`, `move_file` y `mkdir` requieren simultáneamente:

- grant compatible de escritura;
- `writes_enabled=true`;
- Project con escritura habilitada;
- Target/Project activos;
- validación de path/canonicalización correspondiente.

Cualquier gate negativo deniega la operación.

## Trusted Target shell

`run_command` requiere grant compatible con `target_shell`/`run_command`, `shell_enabled=true`, Target/Project activos y auditoría. El Project aporta scope de autorización y cwd inicial; no convierte el shell en un sandbox de filesystem.

## Privilegio administrativo del Target

El privilegio del sistema operativo es una segunda autorización sobre la ejecución existente; no es otra herramienta. `run_command` puede solicitarlo explícitamente y `run_task` también exige esta puerta cuando el transporte base ya está observado como root/Administrator.

Una ejecución con `privilege=required` —o una sesión SSH que el probe observa ya como root/Administrator— necesita además:

- un token **explícito** `target_admin` en un grant que coincida con Target/Project;
- `privilege_policy` del Target distinto de `never`;
- aprobación humana acotada al mismo cliente/proyecto cuando la política sea `ask_always` o `ask_once_per_boot`;
- backend elevado verificado.

Por compatibilidad segura, `*` **no implica el nuevo privilegio administrativo del Target**. Esto evita que un grant wildcard existente adquiera elevación del SO silenciosamente tras una actualización. Si se desea shell + privilegio, puede usarse por ejemplo:

```text
target_shell,target_admin
```

o dos grants separados con el mismo scope.

`admin` conserva exclusivamente su significado de administración del appliance Gateway. `target_admin` es independiente y sólo habilita la segunda puerta de privilegio del Target; una no implica la otra.

Esta segunda puerta controla elevación solicitada o detectable por MCP-Pi; no transforma `run_command` en un sandbox del sistema operativo. En Linux/Windows, el Target puede definir opcionalmente `privilege_user` para una cuenta SSH administrativa separada. Esa identidad sólo se usa después de pasar los gates ordinarios y MCP-Pi la acepta únicamente si el probe observa `root`/`Administrator` en el mismo Target con la misma identidad de host fijada. En Android/Termux se reutiliza Shizuku/`rish` cuando está verificado. Como `target_shell` es un shell arbitrario, el probe ligero también marca la sesión como privilege-capable si la cuenta normal expone una ruta independiente detectable de elevación (por ejemplo `rish`, Windows `sudo` o `sudo -n` funcional en Linux); entonces incluso `privilege=standard` cruza `target_admin` + política/aprobación, aunque el comando siga arrancando con el usuario normal. MCP-Pi no intenta asegurar esta frontera filtrando cadenas de comandos.

## Precedencia fail-closed

```text
ordinary run_command access
        AND
explicit target_admin grant
        AND
Target privilege_policy
        AND
required approval / boot identity
        AND
verified privilege backend
= privileged Target execution
```

Un fallo en cualquiera de estas condiciones produce un error explícito; MCP-Pi no degrada silenciosamente a una ruta menos controlada ni cambia políticas globales de UAC/sudo para hacer pasar la operación.
