# Controlled write architecture

Structured filesystem mutation is a Go Core capability in MCP-Pi 1.6.2. It is separate from trusted Target shell.

## Authorization flow

    MCP request
      -> schema validation
      -> Go Core
      -> gateway/writes kill switches
      -> client grant
      -> Target + Project scope
      -> relative/canonical path policy
      -> remote structured operation
      -> audit
      -> structured response

## Supported mutation tools

- write_file
- append_file
- delete_file
- copy_file
- move_file
- mkdir

write_file supports create/overwrite rules, bounded UTF-8 content, dry-run diff and optimistic expected SHA on overwrite.

## Confinement rules

The Core validates the requested relative path and Project root. Remote operations additionally reject unsafe symlink/reparse states and validate canonical results before mutation.

A structured operation must not silently degrade into run_command. If the remote platform cannot satisfy a required precondition, it is denied.

## Conflict and failure semantics

Representative errors include:
- WRITES_DISABLED;
- TOOL_NOT_ALLOWED;
- PATH_OUTSIDE_ALLOWED_ROOT;
- SYMLINK_WRITE_DENIED;
- NOT_FOUND;
- FILE_ALREADY_EXISTS;
- WRITE_CONFLICT;
- FILE_TOO_LARGE;
- AUDIT_UNAVAILABLE.

Critical mutations require audit availability before execution.

## Backup behavior

Existing-file mutations can create a gateway-side recovery copy before change. Registry backup is a separate SQLite operation and uses the online backup API.

## Emergency control

writes_enabled is the global structured-write kill switch. Disabling it denies write capabilities without changing client grants.
