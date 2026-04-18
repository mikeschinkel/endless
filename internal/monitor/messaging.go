package monitor

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ChannelInfo represents a messaging channel between two sessions.
type ChannelInfo struct {
	ID        int64
	ChannelID string
	SessionA  string
	PaneA     string
	SessionB  string
	PaneB     string
	ProjectID int64
	State     string
	CreatedAt string
}

// MessageInfo represents a queued message.
type MessageInfo struct {
	ID        int64
	ChannelID string
	Sender    string
	Body      string
	Status    string
	CreatedAt string
}

// CreateBeacon registers a new channel in 'beacon' state.
func CreateBeacon(sessionID, tmuxPane string, projectID int64) (string, error) {
	db, err := DB()
	if err != nil {
		return "", err
	}

	channelID := uuid.New().String()[:8]
	now := time.Now().UTC().Format("2006-01-02T15:04:05")

	_, err = db.Exec(
		`INSERT INTO msg_channels (channel_id, session_a, pane_a, project_id, state, created_at)
		 VALUES (?, ?, ?, ?, 'beacon', ?)`,
		channelID, sessionID, tmuxPane, projectID, now,
	)
	if err != nil {
		return "", fmt.Errorf("create beacon: %w", err)
	}
	return channelID, nil
}

// ListBeacons returns all channels in 'beacon' state, optionally filtered by project.
func ListBeacons(projectID int64) ([]ChannelInfo, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}

	query := `SELECT id, channel_id, session_a, pane_a, COALESCE(project_id,0), state, created_at
	           FROM msg_channels WHERE state = 'beacon'`
	args := []any{}
	if projectID > 0 {
		query += " AND project_id = ?"
		args = append(args, projectID)
	}
	query += " ORDER BY created_at DESC"

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []ChannelInfo
	for rows.Next() {
		var c ChannelInfo
		if err := rows.Scan(&c.ID, &c.ChannelID, &c.SessionA, &c.PaneA, &c.ProjectID, &c.State, &c.CreatedAt); err != nil {
			continue
		}
		channels = append(channels, c)
	}
	return channels, nil
}

// ConnectToChannel joins an existing beacon channel.
func ConnectToChannel(channelID, sessionID, tmuxPane string) (*ChannelInfo, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")

	result, err := db.Exec(
		`UPDATE msg_channels SET session_b=?, pane_b=?, state='connected', connected_at=?
		 WHERE channel_id=? AND state='beacon'`,
		sessionID, tmuxPane, now, channelID,
	)
	if err != nil {
		return nil, fmt.Errorf("connect to channel: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return nil, fmt.Errorf("channel %s not found or not in beacon state", channelID)
	}

	// Return the updated channel info
	var c ChannelInfo
	err = db.QueryRow(
		`SELECT id, channel_id, session_a, pane_a, COALESCE(session_b,''), COALESCE(pane_b,''),
		        COALESCE(project_id,0), state, created_at
		 FROM msg_channels WHERE channel_id=?`,
		channelID,
	).Scan(&c.ID, &c.ChannelID, &c.SessionA, &c.PaneA, &c.SessionB, &c.PaneB,
		&c.ProjectID, &c.State, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// SendMessage inserts a message into the queue.
func SendMessage(channelID, senderSessionID, body string) (int64, error) {
	db, err := DB()
	if err != nil {
		return 0, err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")

	result, err := db.Exec(
		`INSERT INTO msg_queue (channel_id, sender, body, status, created_at)
		 VALUES (?, ?, ?, 'queued', ?)`,
		channelID, senderSessionID, body, now,
	)
	if err != nil {
		return 0, fmt.Errorf("send message: %w", err)
	}
	return result.LastInsertId()
}

// HasPendingMessages checks if there are any queued messages for a session without consuming them.
func HasPendingMessages(sessionID string) (bool, error) {
	db, err := DB()
	if err != nil {
		return false, err
	}

	var count int
	err = db.QueryRow(
		`SELECT count(*) FROM msg_queue mq
		 JOIN msg_channels mc ON mq.channel_id = mc.channel_id
		 WHERE mq.status = 'queued'
		   AND mq.sender != ?
		   AND mc.state = 'connected'
		   AND (mc.session_a = ? OR mc.session_b = ?)`,
		sessionID, sessionID, sessionID,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetPendingMessages fetches and marks as delivered all queued messages for a session.
func GetPendingMessages(sessionID string) ([]MessageInfo, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}

	rows, err := db.Query(
		`SELECT mq.id, mq.channel_id, mq.sender, mq.body, mq.status, mq.created_at
		 FROM msg_queue mq
		 JOIN msg_channels mc ON mq.channel_id = mc.channel_id
		 WHERE mq.status = 'queued'
		   AND mq.sender != ?
		   AND mc.state = 'connected'
		   AND (mc.session_a = ? OR mc.session_b = ?)
		 ORDER BY mq.created_at ASC`,
		sessionID, sessionID, sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []MessageInfo
	var ids []any
	for rows.Next() {
		var m MessageInfo
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.Sender, &m.Body, &m.Status, &m.CreatedAt); err != nil {
			continue
		}
		msgs = append(msgs, m)
		ids = append(ids, m.ID)
	}

	// Mark as delivered
	if len(ids) > 0 {
		now := time.Now().UTC().Format("2006-01-02T15:04:05")
		for _, id := range ids {
			db.Exec("UPDATE msg_queue SET status='delivered', delivered_at=? WHERE id=?", now, id)
		}
	}

	return msgs, nil
}

// GetChannelForSession returns the active (connected) channel for a session.
func GetChannelForSession(sessionID string) (*ChannelInfo, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}

	var c ChannelInfo
	err = db.QueryRow(
		`SELECT id, channel_id, session_a, pane_a, COALESCE(session_b,''), COALESCE(pane_b,''),
		        COALESCE(project_id,0), state, created_at
		 FROM msg_channels
		 WHERE state = 'connected'
		   AND (session_a = ? OR session_b = ?)
		 ORDER BY connected_at DESC LIMIT 1`,
		sessionID, sessionID,
	).Scan(&c.ID, &c.ChannelID, &c.SessionA, &c.PaneA, &c.SessionB, &c.PaneB,
		&c.ProjectID, &c.State, &c.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("no active channel for session: %w", err)
	}
	return &c, nil
}

// GetTargetPane returns the tmux pane of the other participant in a channel.
func GetTargetPane(channelID, senderSessionID string) (string, error) {
	db, err := DB()
	if err != nil {
		return "", err
	}

	var sessionA, paneA, sessionB, paneB string
	err = db.QueryRow(
		`SELECT session_a, pane_a, COALESCE(session_b,''), COALESCE(pane_b,'')
		 FROM msg_channels WHERE channel_id=? AND state='connected'`,
		channelID,
	).Scan(&sessionA, &paneA, &sessionB, &paneB)
	if err != nil {
		return "", fmt.Errorf("channel not found: %w", err)
	}

	if senderSessionID == sessionA {
		return paneB, nil
	}
	return paneA, nil
}

// CloseChannel marks a channel as closed.
func CloseChannel(channelID string) error {
	db, err := DB()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	_, err = db.Exec(
		"UPDATE msg_channels SET state='closed', closed_at=? WHERE channel_id=?",
		now, channelID,
	)
	return err
}

// SessionIDForPane returns the session_id for a given tmux pane.
func SessionIDForPane(tmuxPane string) (string, error) {
	db, err := DB()
	if err != nil {
		return "", err
	}

	var sessionID string
	err = db.QueryRow(
		`SELECT session_id FROM ai_sessions
		 WHERE tmux_pane = ? AND state != 'ended'
		 ORDER BY last_activity DESC LIMIT 1`,
		tmuxPane,
	).Scan(&sessionID)
	if err != nil {
		return "", fmt.Errorf("no active session for pane %s: %w", tmuxPane, err)
	}
	return sessionID, nil
}
