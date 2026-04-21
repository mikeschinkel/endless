package monitor

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ConversationInfo represents a messaging conversation between two processes.
type ConversationInfo struct {
	ID             int64
	ConversationID string
	ProcessA       string
	ProcessB       string
	ProjectID      int64
	State          string
	CreatedAt      string
}

// MessageInfo represents a queued message.
type MessageInfo struct {
	ID             int64
	ConversationID string
	Sender         string
	Body           string
	Status         string
	CreatedAt      string
}

// CreateBeacon registers a new conversation in 'beacon' state.
func CreateBeacon(process string, projectID int64) (string, error) {
	db, err := DB()
	if err != nil {
		return "", err
	}

	conversationID := uuid.New().String()[:8]
	now := time.Now().UTC().Format("2006-01-02T15:04:05")

	_, err = db.Exec(
		`INSERT INTO conversations (conversation_id, process_a, project_id, state, created_at)
		 VALUES (?, ?, ?, 'beacon', ?)`,
		conversationID, process, projectID, now,
	)
	if err != nil {
		return "", fmt.Errorf("create beacon: %w", err)
	}
	return conversationID, nil
}

// ListBeacons returns all conversations in 'beacon' state.
func ListBeacons(projectID int64) ([]ConversationInfo, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}

	query := `SELECT id, conversation_id, process_a, COALESCE(project_id,0), state, created_at
	           FROM conversations WHERE state = 'beacon'`
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

	var convos []ConversationInfo
	for rows.Next() {
		var c ConversationInfo
		if err := rows.Scan(&c.ID, &c.ConversationID, &c.ProcessA, &c.ProjectID, &c.State, &c.CreatedAt); err != nil {
			continue
		}
		convos = append(convos, c)
	}
	return convos, nil
}

// ConnectToConversation joins an existing beacon conversation.
func ConnectToConversation(conversationID, process string) (*ConversationInfo, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")

	result, err := db.Exec(
		`UPDATE conversations SET process_b=?, state='connected', connected_at=?
		 WHERE conversation_id=? AND state='beacon'`,
		process, now, conversationID,
	)
	if err != nil {
		return nil, fmt.Errorf("connect to conversation: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return nil, fmt.Errorf("conversation %s not found or not in beacon state", conversationID)
	}

	var c ConversationInfo
	err = db.QueryRow(
		`SELECT id, conversation_id, process_a, COALESCE(process_b,''),
		        COALESCE(project_id,0), state, created_at
		 FROM conversations WHERE conversation_id=?`,
		conversationID,
	).Scan(&c.ID, &c.ConversationID, &c.ProcessA, &c.ProcessB,
		&c.ProjectID, &c.State, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// SendMessage inserts a message into the queue.
func SendMessage(conversationID, senderProcess, body string) (int64, error) {
	db, err := DB()
	if err != nil {
		return 0, err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")

	result, err := db.Exec(
		`INSERT INTO messages (conversation_id, sender, body, status, created_at)
		 VALUES (?, ?, ?, 'queued', ?)`,
		conversationID, senderProcess, body, now,
	)
	if err != nil {
		return 0, fmt.Errorf("send message: %w", err)
	}
	return result.LastInsertId()
}

// HasPendingMessages checks if there are any queued messages for a process without consuming them.
func HasPendingMessages(process string) (bool, error) {
	db, err := DB()
	if err != nil {
		return false, err
	}

	var count int
	err = db.QueryRow(
		`SELECT count(*) FROM messages mq
		 JOIN conversations mc ON mq.conversation_id = mc.conversation_id
		 WHERE mq.status = 'queued'
		   AND mq.sender != ?
		   AND mc.state = 'connected'
		   AND (mc.process_a = ? OR mc.process_b = ?)`,
		process, process, process,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetPendingMessages fetches and marks as delivered all queued messages for a process.
func GetPendingMessages(process string) ([]MessageInfo, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}

	rows, err := db.Query(
		`SELECT mq.id, mq.conversation_id, mq.sender, mq.body, mq.status, mq.created_at
		 FROM messages mq
		 JOIN conversations mc ON mq.conversation_id = mc.conversation_id
		 WHERE mq.status = 'queued'
		   AND mq.sender != ?
		   AND mc.state = 'connected'
		   AND (mc.process_a = ? OR mc.process_b = ?)
		 ORDER BY mq.created_at ASC`,
		process, process, process,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []MessageInfo
	var ids []any
	for rows.Next() {
		var m MessageInfo
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Sender, &m.Body, &m.Status, &m.CreatedAt); err != nil {
			continue
		}
		msgs = append(msgs, m)
		ids = append(ids, m.ID)
	}

	if len(ids) > 0 {
		now := time.Now().UTC().Format("2006-01-02T15:04:05")
		for _, id := range ids {
			db.Exec("UPDATE messages SET status='delivered', delivered_at=? WHERE id=?", now, id)
		}
	}

	return msgs, nil
}

// GetTargetProcess returns the process identifier of the other participant.
func GetTargetProcess(conversationID, senderProcess string) (string, error) {
	db, err := DB()
	if err != nil {
		return "", err
	}

	var processA, processB string
	err = db.QueryRow(
		`SELECT process_a, COALESCE(process_b,'')
		 FROM conversations WHERE conversation_id=? AND state='connected'`,
		conversationID,
	).Scan(&processA, &processB)
	if err != nil {
		return "", fmt.Errorf("conversation not found: %w", err)
	}

	if senderProcess == processA {
		return processB, nil
	}
	return processA, nil
}

// CloseConversation marks a conversation as closed.
func CloseConversation(conversationID string) error {
	db, err := DB()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	_, err = db.Exec(
		"UPDATE conversations SET state='closed', closed_at=? WHERE conversation_id=?",
		now, conversationID,
	)
	return err
}
