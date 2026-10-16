package web

import "net/http"

func (p *Panel) apiEgressLatency(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "方法不支持"})
		return
	}
	_, err := p.Manager.StartEgressLatencyTests()
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"message": "已开始检测所有已导入节点的延迟", "status": p.Manager.Status(p.Version)})
}
