package llm

import "time"

const historyTTL = 2 * time.Hour

// touchMap records when each investigation's history was last used.
type touchMap map[string]time.Time

// touchLocked marks id as in use and drops histories idle longer than historyTTL.
// The caller must hold c.mu.
func (c *Client) touchLocked(id string) {
	now := time.Now()
	c.touched[id] = now
	for k, t := range c.touched {
		if now.Sub(t) > historyTTL {
			delete(c.touched, k)
			delete(c.histories, k)
		}
	}
}

// Forget releases everything kept for a finished investigation.
func (c *Client) Forget(id string) {
	c.mu.Lock()
	delete(c.histories, id)
	delete(c.touched, id)
	c.mu.Unlock()
	if c.budget != nil {
		c.budget.Forget(id)
	}
}
