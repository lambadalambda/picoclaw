package heartbeat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Activity struct {
	Channel     string `json:"channel"`
	ChatID      string `json:"chat_id"`
	UpdatedAtMS int64  `json:"updated_at_ms"`
}

func (a Activity) normalize() Activity {
	a.Channel = strings.TrimSpace(a.Channel)
	a.ChatID = strings.TrimSpace(a.ChatID)
	return a
}

func (a Activity) valid() bool {
	a = a.normalize()
	return a.Channel != "" && a.ChatID != ""
}

func LoadActivity(path string) (Activity, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Activity{}, false, nil
	}

	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Activity{}, false, nil
		}
		return Activity{}, false, err
	}

	var activity Activity
	if err := json.Unmarshal(b, &activity); err != nil {
		return Activity{}, false, err
	}

	activity = activity.normalize()
	if !activity.valid() {
		return Activity{}, false, nil
	}

	return activity, true, nil
}

func SaveActivity(path string, activity Activity) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("activity path is empty")
	}

	activity = activity.normalize()
	if !activity.valid() {
		return fmt.Errorf("activity must include channel and chat_id")
	}

	activity.UpdatedAtMS = time.Now().UnixMilli()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(activity, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmpFile, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		return err
	}

	return os.Rename(tmpName, path)
}

func ActivityPath(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return ""
	}
	return filepath.Join(workspace, "cron", "last_activity.json")
}

func FormatTimeSince(timestampMS int64) string {
	if timestampMS == 0 {
		return "never"
	}

	duration := time.Since(time.UnixMilli(timestampMS))

	if duration < time.Minute {
		return "just now"
	} else if duration < time.Hour {
		mins := int(duration.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	} else if duration < 24*time.Hour {
		hours := int(duration.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	}

	days := int(duration.Hours() / 24)
	if days == 1 {
		return "1 day ago"
	}
	return fmt.Sprintf("%d days ago", days)
}
