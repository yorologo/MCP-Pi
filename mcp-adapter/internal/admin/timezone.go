package admin

import (
	"context"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"

	"mcp-gateway-adapter/internal/registry"
)

const (
	defaultAdminTimezone    = "UTC"
	activityTimestampLayout = "2006-01-02 15:04:05"
	localDateTimeLayout     = "2006-01-02T15:04"
)

func loadAdminLocation(ctx context.Context, store *registry.Store) (*time.Location, string, error) {
	name, err := store.GetSetting(ctx, "admin_timezone", defaultAdminTimezone)
	if err != nil {
		return nil, "", err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultAdminTimezone
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, "", fmt.Errorf("setting admin_timezone has invalid IANA time zone %q: %w", name, err)
	}
	return loc, name, nil
}

func validateAdminTimezone(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("admin_timezone must be a non-empty IANA time zone")
	}
	if _, err := time.LoadLocation(name); err != nil {
		return "", fmt.Errorf("admin_timezone must be a valid IANA time zone")
	}
	return name, nil
}

func localFilterToUTC(raw string, loc *time.Location) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	t, err := time.ParseInLocation(localDateTimeLayout, raw, loc)
	if err != nil {
		return "", fmt.Errorf("invalid local date/time %q", raw)
	}
	return t.UTC().Format(activityTimestampLayout), nil
}

func localizeActivity(items []registry.ActivityEvent, loc *time.Location) ([]registry.ActivityEvent, error) {
	out := make([]registry.ActivityEvent, len(items))
	copy(out, items)
	for i := range out {
		t, err := time.ParseInLocation(activityTimestampLayout, out[i].Timestamp, time.UTC)
		if err != nil {
			return nil, fmt.Errorf("invalid stored activity timestamp %q: %w", out[i].Timestamp, err)
		}
		out[i].Timestamp = t.In(loc).Format(activityTimestampLayout)
	}
	return out, nil
}
