# Never Rest Intern

[繁體中文](README.md) · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md)

NR-Intern is a desktop AI agent written in Go. It keeps working from the current conversation, verified tool results, and persisted state. Simple requests are handled directly, while longer tasks can be planned, executed step by step, and verified.

![NR-Intern conversation interface](images/cap0001.jpg)

## Highlights

- Extensible provider routing for OpenAI-compatible Chat Completions and OpenAI Codex Responses authenticated through ChatGPT/Codex OAuth. When a Codex account has usage-limit reset credits, they can be redeemed from the provider settings; the confirmation shows how many remain and when the next one expires.
- A `Workspace → Project → Session` hierarchy with per-conversation provider and model selection.
- Standing instructions on workspaces and projects: write the working rules once and every conversation and schedule carries them.
- Multiple ordered work plans with drag-and-drop ordering, step states, and tool-backed evidence.
- Configurable Thinking levels; lock plans for sequential work or unlock them to switch between unfinished plans. Different sessions can work concurrently.
- A standalone schedule section: each schedule carries its own recurrence and sandbox, and starts a new conversation on time.
- Durable runs, replayable SSE, streamed responses, and reconnection support. After a UI restart, choose which conversations to reconnect; interrupted work retains a retry option.
- Recovery controls, an optional persistent notification center, pause/resume/cancel-all for runs, and redacted diagnostic exports.
- Global search, safe backup/restore, and a read-only permissions center with pre-restore snapshots.
- Download a configuration bundle with credential fields redacted to reference provider, MCP, reverse-proxy, and service settings on another machine.
- Version information and update checks in the About page.
- You can keep typing while a conversation is running; later messages are stored in the browser's IndexedDB Durable Outbox and submitted in order after the active run ends. Network retries reuse the same Idempotency-Key.
- Automatic context compaction with a history character limit, persistent memory, and configurable memory scopes.
- Optional experimental Memory Space reuses preferences, decisions, and procedures with near-duplicate handling, project-first scopes, compact recall, and failure-triggered memory lookup.
- Per-run input, output, and total token usage, with session totals across retained runs and estimated cost from configurable model prices. No price means tokens only.
- Native file, document, shell, SSH, and planning tools protected by sandboxes and execution approval.
- Memory-isolated projects keep conversations, plans, attachments, and work files **on the RAM disk as they are written** — never on the drive — so they are gone when the app closes or restarts, leaving only the project settings; each project gets a dedicated, size-configurable RAM disk sandbox. Tool calls in these projects skip per-call approval, and creating new ones can be turned off under Experimental features (on by default) without affecting existing projects.
- The compact tool set includes document reading, creation, and conversion; extended tools, tool retrieval, and native or instruction tool-call modes are also available.
- Indexed paging for long conversations, question previews, and section navigation. Cumulative usage uses K/M notation with exact values on hover.
- Bounded, cancellable waiting with `wait_for`, plus `ssh_wait` polling for read-only remote checks so deployments are confirmed by bytes, SHA-256, or service readiness instead of an early upload return.
- `http_fetch` reads external resources: HTML becomes plain text, localhost and private addresses are allowed by default, and the whole tool can be switched off from system settings.
- A built-in MCP client connects to local stdio, legacy SSE, or remote Streamable HTTP servers and places their tools under the same permission and approval flow.
- An optional NetPass reverse proxy can expose the backend API without publishing the desktop control UI.
- Provider enablement, model discovery, tool permissions, and audit records.
- Light and dark appearance with Auto, Traditional Chinese, English, Japanese, and Korean interfaces.
- Desktop support for Windows x64, Windows ARM64, and macOS ARM64.
- On macOS, a status item is installed at startup. The UI can be hidden while work continues, then restored from the menu or by launching the app again.
- On Windows, a Tray Icon is installed at startup. Left-click restores the UI and the context menu can open NR-Intern or exit it, including when the UI uses the browser fallback.
- A device-local memo pad is available from the sidebar for notes that should not be sent to the agent.
- Screen capture is available beside the conversation input. On macOS it opens the system region capture and an editor for rectangles, lines, and text; copying or closing the editor updates the clipboard with the edited image. Windows opens the system snipping interface.

The interface language controls only the UI and an uncustomized default name. The agent infers the user's preferred language from the current message, recent conversation, and explicit preferences.

## Getting started

1. Create a local configuration from `configs/ai-agent/config.example.json`.
2. Configure the provider endpoint, model, and required credentials locally or through the administration interface.
3. Create a workspace, project, and session, then assign work from the conversation view.

Runtime configuration, generated data, and credentials must remain outside version control. See the documents below for the architecture, API, and security design.

Conversations, work status, and settings are stored by the backend. Notifications are off by default and can be enabled in general settings.
Configuration bundles exclude conversations and attachments and are not full backups. Review URLs, account names, and paths before sharing.

Back up before upgrading. Older runs, event files, and inactive memories beyond the retention period are automatically cleaned up; session transcripts remain. Export usage and audit data separately for long-term records. See the [retention rules](docs/ai-agent/DEVELOPMENT.md#長對話讀取與儲存維護).

## Documentation

[Open the documentation website](https://vaderchen.github.io/NR-Intern/)

- [Architecture](https://vaderchen.github.io/NR-Intern/architecture.html)
- [Document tools](https://vaderchen.github.io/NR-Intern/document-tools.html)
- [Development](https://vaderchen.github.io/NR-Intern/development.html)
- [HTTP API](https://vaderchen.github.io/NR-Intern/http-api.html)
- [Security](https://vaderchen.github.io/NR-Intern/security.html)
- [OpenAPI contract](https://vaderchen.github.io/NR-Intern/openapi.html)

## Data security

- The repository contains sample configuration only. Provider, OAuth, MCP, and NetPass keys, SSH credentials, logs, runtime data, and release artifacts are excluded by `.gitignore`.
- Documentation and examples use relative paths, localhost, or example domains; no developer-specific absolute directory is included.
- Never commit API keys, tokens, private keys, certificates, signing identities, or real service addresses.

## Licensing

This project is dual-licensed:

- Open-source use is available under the [GNU General Public License v3.0 only](LICENSE).
- Commercial licensing is available when GPLv3 obligations are unsuitable; see [COMMERCIAL-LICENSE.md](COMMERCIAL-LICENSE.md).

Third-party components remain subject to their respective bundled licenses.
