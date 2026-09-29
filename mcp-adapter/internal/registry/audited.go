package registry

import (
	"context"
	"database/sql"
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

// UpdateGrantForClientAudited updates only a Grant that belongs to clientID
// and records the successful authorization change atomically.
func (s *Store) UpdateGrantForClientAudited(ctx context.Context, clientID string, grant Grant, activity Activity) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := updateGrantExec(ctx, tx, clientID, grant); err != nil {
			return err
		}
		return recordActivityExec(ctx, tx, activity)
	})
}

// DeleteGrantForClientAudited deletes only a Grant that belongs to clientID
// and records the successful authorization change atomically.
func (s *Store) DeleteGrantForClientAudited(ctx context.Context, clientID string, grantID int64, activity Activity) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := deleteGrantExec(ctx, tx, clientID, grantID); err != nil {
			return err
		}
		return recordActivityExec(ctx, tx, activity)
	})
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
