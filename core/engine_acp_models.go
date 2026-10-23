package core

import (
	"fmt"
	"strconv"
	"strings"
)

// isLiveSessionModelAgent distinguishes standard ACP per-session model RPCs
// from vendor slash commands and legacy agent-wide ModelSwitcher settings.
func isLiveSessionModelAgent(agent Agent) bool {
	capable, ok := agent.(SessionModelSwitchAgent)
	return ok && capable.UsesSessionModelSwitch()
}

func (e *Engine) activeSessionModelSwitcher(sessionKey string) (LiveSessionModelSwitcher, bool) {
	key := e.interactiveKeyForSessionKey(sessionKey)
	e.interactiveMu.Lock()
	state := e.interactiveStates[key]
	e.interactiveMu.Unlock()
	if state == nil {
		return nil, false
	}
	state.mu.Lock()
	session := state.agentSession
	state.mu.Unlock()
	if session == nil || !session.Alive() {
		return nil, false
	}
	v, ok := session.(LiveSessionModelSwitcher)
	return v, ok
}

// collectSessionModelDetails prefers the exact current session, which may
// differ from other chats using the same backend. Fallback uses only the
// last handshaken agent catalog and does not guess a current model.
func (e *Engine) collectSessionModelDetails(sessionKey string, agent Agent) ([]modelGroup, int) {
	if isLiveSessionModelAgent(agent) {
		if live, ok := e.activeSessionModelSwitcher(sessionKey); ok {
			// Empty active-session catalog is authoritative; never fall back
			// to choices from another chat's cached agent catalog.
			return groupSessionModelDetails(live.SessionModelDetails(), e.i18n.T(MsgModelListTitle))
		}
	}
	groups, total := e.collectModelDetails(agent)
	if isLiveSessionModelAgent(agent) {
		for i := range groups {
			for j := range groups[i].details {
				groups[i].details[j].Current = false
			}
		}
	}
	return groups, total
}

func groupSessionModelDetails(details []ModelDetail, heading string) ([]modelGroup, int) {
	if len(details) == 0 {
		return nil, 0
	}
	return []modelGroup{{title: heading, details: details}}, len(details)
}

func (e *Engine) switchLiveSessionModel(sessionKey, modelID string) (string, error) {
	agent, sessions := e.sessionContextForKey(sessionKey)
	if !isLiveSessionModelAgent(agent) {
		return "", fmt.Errorf("backend does not support live session model switch")
	}
	active := sessions.GetOrCreateActive(sessionKey)
	generation, locked := active.TryLock()
	if !locked {
		return "", fmt.Errorf("%s", e.i18n.T(MsgPreviousProcessing))
	}
	defer active.Unlock(generation)
	live, ok := e.activeSessionModelSwitcher(sessionKey)
	if !ok {
		return "", fmt.Errorf("%s", e.i18n.T(MsgModelNoSession))
	}
	details := live.SessionModelDetails()
	display := ""
	for _, d := range details {
		if d.Name == modelID {
			display = d.DisplayName
			break
		}
	}
	if display == "" {
		for _, d := range details {
			if d.Name == modelID {
				display = modelID
				break
			}
		}
	}
	if display == "" {
		return "", fmt.Errorf("unknown model ID %q", modelID)
	}
	if err := live.SwitchSessionModel(modelID); err != nil {
		return "", err
	}
	return display, nil
}

func (e *Engine) cmdLiveSessionModel(p Platform, msg *Message, args []string, agent Agent) {
	if len(args) == 0 {
		if supportsCards(p) {
			e.replyWithCard(p, msg.ReplyCtx, e.renderModelsCard(msg.SessionKey, agent))
		} else {
			e.cmdModelsText(p, msg, agent)
		}
		return
	}
	requested, valid := parseModelSwitchArgs(args)
	if !valid {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgModelUsage))
		return
	}
	groups, _ := e.collectSessionModelDetails(msg.SessionKey, agent)
	var matches []ModelDetail
	for _, g := range groups {
		matches = append(matches, g.details...)
	}
	requested = strings.TrimSpace(requested)
	if n, err := strconv.Atoi(requested); err == nil && n > 0 && n <= len(matches) {
		requested = matches[n-1].Name
	} else {
		for _, d := range matches {
			if strings.EqualFold(requested, d.Name) || (d.DisplayName != "" && strings.EqualFold(requested, d.DisplayName)) {
				requested = d.Name
				break
			}
		}
	}
	label, err := e.switchLiveSessionModel(msg.SessionKey, requested)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgModelChangeFailed, err))
		return
	}
	e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgModelChanged, label))
}
