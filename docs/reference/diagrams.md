# Architecture diagrams

## System architecture

    MCP client ---> Go MCP server ----+
                                      |
    Admin browser -> Go Admin --------+-> Go Core -> Policy/Registry -> SSH -> Target
                                                     |
                                                     +-> Audit

## Component boundaries

    one Go process
      MCP transports
      Core / policy
      Admin
      Registry migrations
      Doctor
      backup/restore
      maintenance

    OS boundaries
      systemd -> services/timer/postboot
      logind + polkit -> controlled reboot
      OpenSSH -> pinned Targets

## MCP tool-call sequence

    Client -> MCP server: tools/call
    MCP server -> Core: bound client + tool + args
    Core -> Registry: authorization/settings
    Registry -> Core: allow/deny
    Core -> Target: bounded remote operation when allowed
    Core -> Registry: audit
    Core -> MCP server: result/error
    MCP server -> Client: MCP response

## Deployment topology

    Git exact SHA
      -> canonical ARMv6 release bundle
      -> remote candidate validation
      -> Registry online backup
      -> controlled service stop
      -> root-owned runtime activation
      -> Go Registry migration/Doctor
      -> service acceptance
      -> verified provenance

Rollback restores Registry and previous runtime/system assets before service acceptance.
