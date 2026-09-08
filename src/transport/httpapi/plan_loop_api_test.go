package httpapi_test

import (
	"net/http"
	"testing"

	"AgenticService/src/bootstrap"
)

// LOOP 的四個端點在發佈之前沒有任何人走過——單元測試守著領域規則，
// 但「路由存在、參數對得上、狀態真的寫回計畫」只有從 handler 這一層看得到。
// 這裡刻意用完整的 bootstrap runtime，所以連 planLoopBinding 的接線一起涵蓋。
func TestPlanLoopEndpointsDriveTheLoopState(t *testing.T) {
	runtime := testRuntime(t, "")
	sessionID, planID := loopFixture(t, runtime)

	started, body := call(t, runtime, http.MethodPost,
		"/api/v1/sessions/"+sessionID+"/plans/"+planID+"/loop", `{"max_rounds":2}`)
	if started.Code != http.StatusOK {
		t.Fatalf("啟動 LOOP = %d，body = %s", started.Code, started.Body.String())
	}
	loop := planLoopField(t, body)
	// Round 記的是「已開始」的次數，所以啟動後立刻就是 1——這一輪的成本已經花掉了。
	if loop["round"] != float64(1) || loop["max_rounds"] != float64(2) {
		t.Fatalf("啟動後的 LOOP 狀態 = %v", loop)
	}

	stopped, stopBody := call(t, runtime, http.MethodDelete,
		"/api/v1/sessions/"+sessionID+"/plans/"+planID+"/loop", "")
	if stopped.Code != http.StatusOK {
		t.Fatalf("停止 LOOP = %d，body = %s", stopped.Code, stopped.Body.String())
	}
	if status := planLoopField(t, stopBody)["status"]; status != "stopped" {
		t.Fatalf("停止後的狀態 = %v", status)
	}

	// 停掉的 LOOP 不能續跑：那會讓使用者以為還在跑，實際上輪數已經結算過。
	resumed, _ := call(t, runtime, http.MethodPost,
		"/api/v1/sessions/"+sessionID+"/plans/"+planID+"/loop/resume", "")
	if resumed.Code < 400 {
		t.Fatalf("續跑已停止的 LOOP 應被拒絕，得到 %d：%s", resumed.Code, resumed.Body.String())
	}
}

// 硬上限要在 HTTP 這一層就擋下來。Agent 沒有這個端點，但使用者介面有，
// 而「20」這個數字存在的理由是使用者也可能高估自己的判斷。
func TestPlanLoopEndpointRejectsRoundsBeyondTheHardCap(t *testing.T) {
	runtime := testRuntime(t, "")
	sessionID, planID := loopFixture(t, runtime)

	recorder, _ := call(t, runtime, http.MethodPost,
		"/api/v1/sessions/"+sessionID+"/plans/"+planID+"/loop", `{"max_rounds":999}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("超過硬上限應回 400，得到 %d：%s", recorder.Code, recorder.Body.String())
	}
}

// 沒有這個計畫時要回 404，而不是預設出一個空的 LOOP。
func TestPlanLoopEndpointRejectsUnknownPlan(t *testing.T) {
	runtime := testRuntime(t, "")
	sessionID, _ := loopFixture(t, runtime)

	recorder, _ := call(t, runtime, http.MethodPost,
		"/api/v1/sessions/"+sessionID+"/plans/plan_missing/loop", `{"max_rounds":2}`)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("未知計畫應回 404，得到 %d：%s", recorder.Code, recorder.Body.String())
	}
}

func loopFixture(t *testing.T, runtime *bootstrap.Runtime) (string, string) {
	t.Helper()
	workspace, workspaceBody := call(t, runtime, http.MethodPost, "/api/v1/workspaces", `{
		"name":"loop",
		"provider_ids":["openai-compatible"],
		"default_provider_id":"openai-compatible"
	}`)
	if workspace.Code != http.StatusCreated && workspace.Code != http.StatusOK {
		t.Fatalf("建立 Workspace = %d，body = %s", workspace.Code, workspace.Body.String())
	}
	workspaceID := stringField(t, workspaceBody, "id")

	session, sessionBody := call(t, runtime, http.MethodPost, "/api/v1/agents/general-agent/sessions",
		`{"title":"LOOP 接線驗證","workspace_id":"`+workspaceID+`"}`)
	if session.Code != http.StatusCreated && session.Code != http.StatusOK {
		t.Fatalf("建立 Session = %d，body = %s", session.Code, session.Body.String())
	}
	sessionID := stringField(t, sessionBody, "id")

	plan, planBody := call(t, runtime, http.MethodPost, "/api/v1/sessions/"+sessionID+"/plans", `{
		"title":"接線驗證",
		"objective":"確認 LOOP 端點真的接得上",
		"steps":[{"title":"步驟一","verification":"條件一"}]
	}`)
	if plan.Code != http.StatusCreated && plan.Code != http.StatusOK {
		t.Fatalf("建立計畫 = %d，body = %s", plan.Code, plan.Body.String())
	}
	return sessionID, stringField(t, planBody, "id")
}

func stringField(t *testing.T, body map[string]any, name string) string {
	t.Helper()
	data := memoryData(t, body)
	value, _ := data[name].(string)
	if value == "" {
		t.Fatalf("回應缺少 %q：%v", name, data)
	}
	return value
}

func planLoopField(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	loop, _ := memoryData(t, body)["loop"].(map[string]any)
	if loop == nil {
		t.Fatalf("回應缺少 loop 欄位：%v", body)
	}
	return loop
}
