package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"AgenticService/src/bootstrap"
)

// 專案底下有對話時，預設拒絕是對的：那是使用者的工作紀錄，不該因為刪一個分類
// 就一起消失。但拒絕之後唯一的出路是手動一則一則刪，對話多的時候等於刪不掉。
func TestDeleteProjectRefusesWhileSessionsExist(t *testing.T) {
	runtime := testRuntime(t, "")
	projectID := seedProjectWithSession(t, runtime)

	recorder, _ := call(t, runtime, http.MethodDelete, "/api/v1/projects/"+projectID, "")
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 while the project still holds sessions; body = %s", recorder.Code, recorder.Body.String())
	}
	// 拒絕之後專案必須還在，不能刪一半。
	if listed, _ := call(t, runtime, http.MethodGet, "/api/v1/projects", ""); !containsProject(t, listed.Body.Bytes(), projectID) {
		t.Fatal("a refused delete must leave the project in place")
	}
}

// force 讓使用者在知道後果的前提下一次完成。
func TestDeleteProjectForceRemovesItsSessions(t *testing.T) {
	runtime := testRuntime(t, "")
	projectID := seedProjectWithSession(t, runtime)

	recorder, _ := call(t, runtime, http.MethodDelete, "/api/v1/projects/"+projectID+"?force=true", "")
	if recorder.Code != http.StatusNoContent && recorder.Code != http.StatusOK {
		t.Fatalf("force delete status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if listed, _ := call(t, runtime, http.MethodGet, "/api/v1/projects", ""); containsProject(t, listed.Body.Bytes(), projectID) {
		t.Fatal("the project should be gone after a force delete")
	}
	// 對話也要真的消失，不能留下指向不存在專案的孤兒。
	listed, _ := call(t, runtime, http.MethodGet, "/api/v1/agents/general-agent/sessions", "")
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	for _, session := range payload.Data {
		if fmt.Sprint(session["project_id"]) == projectID {
			t.Fatalf("an orphan session survived the force delete: %v", session["id"])
		}
	}
}

// force=false 與省略參數的行為必須一致，別讓一個打錯的查詢字串變成刪光資料。
func TestDeleteProjectForceMustBeExplicit(t *testing.T) {
	runtime := testRuntime(t, "")
	projectID := seedProjectWithSession(t, runtime)
	for _, query := range []string{"?force=false", "?force=", "?force=1", "?force=yes"} {
		recorder, _ := call(t, runtime, http.MethodDelete, "/api/v1/projects/"+projectID+query, "")
		if recorder.Code != http.StatusConflict {
			t.Fatalf("%q gave status %d, want 409; only force=true may cascade", query, recorder.Code)
		}
	}
}

func seedProjectWithSession(t *testing.T, runtime *bootstrap.Runtime) string {
	t.Helper()
	listed, workspaces := call(t, runtime, http.MethodGet, "/api/v1/workspaces", "")
	if listed.Code != http.StatusOK {
		t.Fatalf("list workspaces: status %d", listed.Code)
	}
	items, _ := workspaces["data"].([]any)
	if len(items) == 0 {
		t.Fatal("the runtime should start with a default workspace")
	}
	workspace, _ := items[0].(map[string]any)
	workspaceID := fmt.Sprint(workspace["id"])

	created, decoded := call(t, runtime, http.MethodPost, "/api/v1/projects",
		fmt.Sprintf(`{"name":"SmallTalk","workspace_id":%q}`, workspaceID))
	if created.Code != http.StatusCreated {
		t.Fatalf("create project: status %d body %s", created.Code, created.Body.String())
	}
	project, _ := decoded["data"].(map[string]any)
	projectID := fmt.Sprint(project["id"])
	if projectID == "" || projectID == "<nil>" {
		t.Fatalf("project has no id: %v", decoded)
	}
	session, _ := call(t, runtime, http.MethodPost, "/api/v1/agents/general-agent/sessions",
		fmt.Sprintf(`{"title":"聊天","project_id":%q,"workspace_id":%q}`, projectID, workspaceID))
	if session.Code != http.StatusCreated {
		t.Fatalf("create session: status %d body %s", session.Code, session.Body.String())
	}
	return projectID
}

func containsProject(t *testing.T, body []byte, projectID string) bool {
	t.Helper()
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode projects: %v", err)
	}
	for _, project := range payload.Data {
		if fmt.Sprint(project["id"]) == projectID {
			return true
		}
	}
	return false
}
