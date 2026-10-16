package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

func (p *Panel) apiSubscriptions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if id := r.URL.Query().Get("id"); id != "" {
			data, err := p.Manager.SubscriptionCandidates(id)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, data)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"subscriptions": p.Manager.Subscriptions()})
	case http.MethodPost:
		var body struct {
			Action   string   `json:"action"`
			ID       string   `json:"id"`
			Name     string   `json:"name"`
			URL      string   `json:"url"`
			Revision string   `json:"revision"`
			Keys     []string `json:"keys"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 48<<10)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
			return
		}
		var message string
		var err error
		if body.Action == "import" {
			message, err = p.Manager.ImportSubscription(r.Context(), body.ID, body.Revision, body.Keys)
		} else {
			message, err = p.Manager.ChangeSubscription(r.Context(), body.Action, body.ID, body.Name, body.URL)
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"message": message, "subscriptions": p.Manager.Subscriptions(), "status": p.Manager.Status(p.Version)})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "方法不支持"})
	}
}

func (p *Panel) apiLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "方法不支持"})
		return
	}
	lines := 200
	query, qerr := url.ParseQuery(r.URL.RawQuery)
	if qerr != nil || len(query["lines"]) > 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "日志参数无效"})
		return
	}
	if raw := query.Get("lines"); raw != "" {
		var err error
		lines, err = strconv.Atoi(raw)
		if err != nil || lines < 1 || lines > 500 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "日志条数必须在 1–500 之间"})
			return
		}
	}
	logs, err := p.Manager.ServiceLogs(r.Context(), lines)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, logs)
}
