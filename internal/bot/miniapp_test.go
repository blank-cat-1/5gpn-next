package bot

import (
	"encoding/json"
	"testing"
)

func TestMiniAppKeyboardUsesWebAppInsteadOfOrdinaryLink(t *testing.T) {
	var keyboard struct {
		Rows [][]struct {
			WebApp *struct {
				URL string `json:"url"`
			} `json:"web_app"`
			URL      string `json:"url"`
			Callback string `json:"callback_data"`
		} `json:"inline_keyboard"`
	}
	err := json.Unmarshal([]byte(inlineKeyboard([]btn{{"管理", "webapp:https://panel.example/"}, {"原链接", "url:https://example.com/"}})), &keyboard)
	if err != nil {
		t.Fatal(err)
	}
	mini := keyboard.Rows[0][0]
	if mini.WebApp == nil || mini.WebApp.URL != "https://panel.example/" || mini.URL != "" || mini.Callback != "" {
		t.Fatalf("invalid Mini App button: %+v", mini)
	}
	if keyboard.Rows[0][1].URL != "https://example.com/" {
		t.Fatal("legacy URL button broken")
	}
}
