package registry

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// AddTargetAudited creates a Target and records the successful mutation atomically.
func (s *Store) AddTargetAudited(ctx context.Context, target Target, activity Activity) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := addTargetExec(ctx, tx, target); err != nil {
			return err
		}
		return recordActivityExec(ctx, tx, activity)
	})
}

// AddProjectAudited creates a Project and records the successful mutation atomically.
func (s *Store) AddProjectAudited(ctx context.Context, project Project, activity Activity) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := addProjectExec(ctx, tx, project); err != nil {
			return err
		}
		return recordActivityExec(ctx, tx, activity)
	})
}

// AddTaskAudited creates an allowlisted Project task and records the mutation atomically.
func (s *Store) AddTaskAudited(ctx context.Context, targetID, projectID, taskName string, task Task, activity Activity) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := addTaskExec(ctx, tx, targetID, projectID, taskName, task); err != nil {
			return err
		}
		return recordActivityExec(ctx, tx, activity)
	})
}

// UpdateTaskAudited updates an allowlisted Project task and records the mutation atomically.
func (s *Store) UpdateTaskAudited(ctx context.Context, targetID, projectID, taskName string, task Task, activity Activity) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := updateTaskExec(ctx, tx, targetID, projectID, taskName, task); err != nil {
			return err
		}
		return recordActivityExec(ctx, tx, activity)
	})
}

// DeleteTaskAudited removes an allowlisted Project task and records the mutation atomically.
func (s *Store) DeleteTaskAudited(ctx context.Context, targetID, projectID, taskName string, activity Activity) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := deleteTaskExec(ctx, tx, targetID, projectID, taskName); err != nil {
			return err
		}
		return recordActivityExec(ctx, tx, activity)
	})
}

// AddClientAudited creates an AI client and records the successful mutation atomically.
func (s *Store) AddClientAudited(ctx context.Context, client Client, activity Activity) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := addClientExec(ctx, tx, client); err != nil {
			return err
		}
		return recordActivityExec(ctx, tx, activity)
	})
}

// UpdateTargetAudited applies a Target security-context change, revokes cached
// approvals and records the successful mutation in one SQLite transaction.
func (s *Store) UpdateTargetAudited(ctx context.Context, target Target, activity Activity) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := updateTargetExec(ctx, tx, target); err != nil {
			return err
		}
		if err := clearPrivilegeApprovalsScopedExec(ctx, tx, strings.TrimSpace(target.ID), "", ""); err != nil {
			return err
		}
		return recordActivityExec(ctx, tx, activity)
	})
}

// UpdateTargetEndpointAudited atomically moves only a Target's mutable SSH
// endpoint when the caller still observes the expected old endpoint. It does
// not revoke privilege approvals because Target identity/security context is
// unchanged; the successful mutation and its Activity record commit together.
func (s *Store) UpdateTargetEndpointAudited(
	ctx context.Context,
	targetID, expectedHost string,
	expectedPort int,
	newHost string,
	newPort int,
	activity Activity,
) error {
	targetID = strings.TrimSpace(targetID)
	expectedHost = strings.TrimSpace(expectedHost)
	newHost = strings.TrimSpace(newHost)
	if _, err := ValidateStableID(targetID); err != nil {
		return fmt.Errorf("invalid target id: %w", err)
	}
	if expectedHost == "" || newHost == "" {
		return fmt.Errorf("target endpoint host cannot be empty")
	}
	if expectedPort <= 0 || expectedPort > 65535 || newPort <= 0 || newPort > 65535 {
		return fmt.Errorf("target endpoint port must be between 1 and 65535")
	}

	return s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
UPDATE targets
SET host = ?, port = ?
WHERE id = ? AND host = ? AND port = ?
`, newHost, newPort, targetID, expectedHost, expectedPort)
		if err != nil {
			return fmt.Errorf("update target endpoint: %w", err)
		}
		rows, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("inspect target endpoint update: %w", err)
		}
		if rows != 1 {
			return &LookupError{
				Code:    "TARGET_ENDPOINT_CONFLICT",
				Message: fmt.Sprintf("target '%s' endpoint changed concurrently or no longer exists", targetID),
			}
		}
		return recordActivityExec(ctx, tx, activity)
	})
}

// UpdateProjectAudited updates Project permissions/state, revokes approvals
// scoped to that Project and records the successful mutation atomically.
func (s *Store) UpdateProjectAudited(ctx context.Context, project Project, activity Activity) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := updateProjectExec(ctx, tx, project); err != nil {
			return err
		}
		if err := clearPrivilegeApprovalsScopedExec(ctx, tx, strings.TrimSpace(project.TargetID), "", strings.TrimSpace(project.ID)); err != nil {
			return err
		}
		return recordActivityExec(ctx, tx, activity)
	})
}

// UpdateClientAudited updates the client and, when disabling it, revokes
// cached approvals before recording the successful mutation atomically.
func (s *Store) UpdateClientAudited(ctx context.Context, client Client, activity Activity) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := updateClientExec(ctx, tx, client); err != nil {
			return err
		}
		if !client.Enabled {
			if err := clearPrivilegeApprovalsScopedExec(ctx, tx, "", strings.TrimSpace(client.ID), ""); err != nil {
				return err
			}
		}
		return recordActivityExec(ctx, tx, activity)
	})
}

// AddGrantAudited creates one explicit Grant and records the successful
// authorization change atomically.
func (s *Store) AddGrantAudited(ctx context.Context, grant Grant, activity Activity) (int64, error) {
	var id int64
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var err error
		id, err = addGrantExec(ctx, tx, grant)
		if err != nil {
			return err
		}
		return recordActivityExec(ctx, tx, activity)
	})
	return id, err
}

// AddGrantsAudited creates explicit Grants and records one granular Activity
// per Grant in a single SQLite transaction. Any insert or audit failure rolls
// back the complete batch.
func (s *Store) AddGrantsAudited(ctx context.Context, grants []Grant, activities []Activity) ([]int64, error) {
	if len(grants) == 0 {
		return nil, fmt.Errorf("grant batch cannot be empty")
	}
	if len(grants) != len(activities) {
		return nil, fmt.Errorf("grant/activity batch length mismatch")
	}
	ids := make([]int64, 0, len(grants))
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		for i, grant := range grants {
			id, err := addGrantExec(ctx, tx, grant)
			if err != nil {
				return err
			}
			if err := recordActivityExec(ctx, tx, activities[i]); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func normalizeGrantScopeCapabilities(values []string, requireNonEmpty bool) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		capability, err := normalizeGrantCapability(raw)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[capability]; exists {
			continue
		}
		seen[capability] = struct{}{}
		out = append(out, capability)
	}
	sort.Strings(out)
	if requireNonEmpty && len(out) == 0 {
		return nil, fmt.Errorf("grant scope capability set cannot be empty")
	}
	return out, nil
}

func sameGrantCapabilitySet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func listGrantScopeTx(ctx context.Context, tx *sql.Tx, clientID, targetID, projectID string) ([]Grant, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT id, client_id, target_id, project_id, capability, enabled, created_at
FROM grants
WHERE client_id = ?
  AND COALESCE(target_id, '*') = ?
  AND COALESCE(project_id, '*') = ?
ORDER BY id
`, clientID, targetID, projectID)
	if err != nil {
		return nil, fmt.Errorf("list grant scope: %w", err)
	}
	defer rows.Close()

	var grants []Grant
	for rows.Next() {
		grant, err := scanGrant(rows)
		if err != nil {
			return nil, fmt.Errorf("scan grant scope: %w", err)
		}
		grants = append(grants, grant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate grant scope: %w", err)
	}
	return grants, nil
}

func requireExpectedGrantScopeTx(
	ctx context.Context,
	tx *sql.Tx,
	clientID, targetID, projectID string,
	expectedCapabilities []string,
) ([]Grant, []string, error) {
	current, err := listGrantScopeTx(ctx, tx, clientID, targetID, projectID)
	if err != nil {
		return nil, nil, err
	}
	currentCapabilities := make([]string, 0, len(current))
	for _, grant := range current {
		currentCapabilities = append(currentCapabilities, strings.TrimSpace(grant.Capability))
	}
	sort.Strings(currentCapabilities)
	if !sameGrantCapabilitySet(currentCapabilities, expectedCapabilities) {
		return nil, nil, &LookupError{
			Code:    "GRANT_SCOPE_CONFLICT",
			Message: "grant scope changed while it was being modified; reload before retrying",
		}
	}
	return current, currentCapabilities, nil
}

// SyncGrantScopeAudited synchronizes the capabilities stored for one exact
// client/Target/Project scope. Existing Grants that remain selected are left
// untouched so their IDs and Enabled state are preserved. The expected set is
// checked inside the same transaction to fail closed on stale form updates.
func (s *Store) SyncGrantScopeAudited(
	ctx context.Context,
	clientID, targetID, projectID string,
	expectedCapabilities, desiredCapabilities []string,
	baseActivity Activity,
) ([]string, []string, error) {
	clientID = strings.TrimSpace(clientID)
	targetID = strings.TrimSpace(targetID)
	projectID = strings.TrimSpace(projectID)
	if clientID == "" {
		return nil, nil, fmt.Errorf("client id cannot be empty")
	}
	if targetID == "" {
		return nil, nil, fmt.Errorf("target id cannot be empty")
	}
	if projectID == "" {
		projectID = "*"
	}

	expected, err := normalizeGrantScopeCapabilities(expectedCapabilities, true)
	if err != nil {
		return nil, nil, fmt.Errorf("normalize expected grant scope: %w", err)
	}
	desired, err := normalizeGrantScopeCapabilities(desiredCapabilities, false)
	if err != nil {
		return nil, nil, fmt.Errorf("normalize desired grant scope: %w", err)
	}

	added := []string{}
	removed := []string{}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		current, currentCapabilities, err := requireExpectedGrantScopeTx(ctx, tx, clientID, targetID, projectID, expected)
		if err != nil {
			return err
		}
		currentByCapability := make(map[string]Grant, len(current))
		for _, grant := range current {
			currentByCapability[strings.TrimSpace(grant.Capability)] = grant
		}

		desiredSet := make(map[string]struct{}, len(desired))
		for _, capability := range desired {
			desiredSet[capability] = struct{}{}
		}

		for _, capability := range currentCapabilities {
			if _, keep := desiredSet[capability]; keep {
				continue
			}
			grant := currentByCapability[capability]
			if err := deleteGrantExec(ctx, tx, clientID, grant.ID); err != nil {
				return err
			}
			activity := baseActivity
			activity.Action = "delete_grant"
			activity.Success = true
			activity.Detail = fmt.Sprintf("Deleted grant '%s' for client '%s' via access scope synchronization", capability, clientID)
			if err := recordActivityExec(ctx, tx, activity); err != nil {
				return err
			}
			removed = append(removed, capability)
		}

		for _, capability := range desired {
			if _, exists := currentByCapability[capability]; exists {
				continue
			}
			if _, err := addGrantExec(ctx, tx, Grant{
				ClientID: clientID, TargetID: targetID, ProjectID: projectID,
				Capability: capability, Enabled: true,
			}); err != nil {
				return err
			}
			activity := baseActivity
			activity.Action = "add_grant"
			activity.Success = true
			activity.Detail = fmt.Sprintf("Added grant '%s' for client '%s' via access scope synchronization", capability, clientID)
			if err := recordActivityExec(ctx, tx, activity); err != nil {
				return err
			}
			added = append(added, capability)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return added, removed, nil
}

// SetGrantScopeEnabledAudited sets Enabled consistently for one exact
// client/Target/Project scope. The expected capability set is checked inside
// the same transaction so a stale grouped action cannot affect unseen Grants.
func (s *Store) SetGrantScopeEnabledAudited(
	ctx context.Context,
	clientID, targetID, projectID string,
	expectedCapabilities []string,
	enabled bool,
	baseActivity Activity,
) (int, error) {
	clientID = strings.TrimSpace(clientID)
	targetID = strings.TrimSpace(targetID)
	projectID = strings.TrimSpace(projectID)
	if clientID == "" {
		return 0, fmt.Errorf("client id cannot be empty")
	}
	if targetID == "" {
		return 0, fmt.Errorf("target id cannot be empty")
	}
	if projectID == "" {
		projectID = "*"
	}

	expected, err := normalizeGrantScopeCapabilities(expectedCapabilities, true)
	if err != nil {
		return 0, fmt.Errorf("normalize expected grant scope: %w", err)
	}

	changed := 0
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		current, _, err := requireExpectedGrantScopeTx(ctx, tx, clientID, targetID, projectID, expected)
		if err != nil {
			return err
		}

		for _, grant := range current {
			if grant.Enabled == enabled {
				continue
			}
			grant.Enabled = enabled
			if err := updateGrantExec(ctx, tx, clientID, grant); err != nil {
				return err
			}
			activity := baseActivity
			activity.Action = "set_grant_enabled"
			activity.Success = true
			activity.Detail = fmt.Sprintf("Set grant '%s' enabled=%v for client '%s' via access scope", grant.Capability, enabled, clientID)
			if err := recordActivityExec(ctx, tx, activity); err != nil {
				return err
			}
			changed++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return changed, nil
}

func (s *Store) SetPrivilegeApprovalAudited(ctx context.Context, targetID, policy, clientID, projectID, bootID string, activity Activity) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := setPrivilegeApprovalExec(ctx, tx, targetID, policy, clientID, projectID, bootID); err != nil {
			return err
		}
		return recordActivityExec(ctx, tx, activity)
	})
}

func (s *Store) ClearPrivilegeApprovalAudited(ctx context.Context, targetID string, activity Activity) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := clearPrivilegeApprovalsScopedExec(ctx, tx, targetID, "", ""); err != nil {
			return err
		}
		return recordActivityExec(ctx, tx, activity)
	})
}

func (s *Store) SetSettingAudited(ctx context.Context, key, value string, activity Activity) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := setSettingExec(ctx, tx, key, value); err != nil {
			return err
		}
		return recordActivityExec(ctx, tx, activity)
	})
}

func (s *Store) SetSettingsAudited(ctx context.Context, values map[string]string, activity Activity) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := setSettingsExec(ctx, tx, values); err != nil {
			return err
		}
		return recordActivityExec(ctx, tx, activity)
	})
}
