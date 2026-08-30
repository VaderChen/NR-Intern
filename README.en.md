# Never Rest Intern

[繁體中文](README.md) · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md)

NR-Intern is a desktop AI agent written in Go. It keeps working from the current conversation, verified tool results, and persisted state. Simple requests are handled directly, while longer tasks can be planned, executed step by step, and verified.

## Highlights

- Extensible provider routing for OpenAI-compatible Chat Completions and OpenAI Codex Responses authenticated through ChatGPT/Codex OAuth.
- A `Workspace → Project → Session` hierarchy with per-conversation provider and model selection.
- Standing instructions on workspaces and projects: write the working rules once and every conversation and schedule carries them.
- Multiple ordered work plans with drag-and-drop ordering, step states, and tool-backed evidence.
- A standalone schedule section: each schedule carries its own recurrence and sandbox, and starts a new conversation on time.
- Durable runs, replayable SSE, streamed responses, and reconnection support.
- You can keep typing while a conversation is running; later messages stay in the current UI's send queue and are submitted in order after the active run ends.
- Automatic context compaction, cross-session memory, and configurable memory scopes.
- Native file, document, shell, SSH, and planning tools protected by sandboxes and execution approval.
- `http_fetch` reads external resources: HTML becomes plain text, private addresses are refused by default, and the whole tool can be switched off from system settings.
- A built-in MCP client connects to local stdio or remote Streamable HTTP servers and places their tools under the same permission and approval flow.
- An optional NetPass reverse proxy can expose the backend API without publishing the desktop control UI.
- Provider enablement, model discovery, tool permissions, and audit records.
- Light and dark appearance with Auto, Traditional Chinese, English, Japanese, and Korean interfaces.
- Desktop support for Windows x64, Windows ARM64, and macOS ARM64.
- On macOS, a status item is installed at startup. The UI can be hidden while work continues, then restored from the menu or by launching the app again.

The interface language controls only the UI and an uncustomized default name. The agent infers the user's preferred language from the current message, recent conversation, and explicit preferences.

## Getting started

1. Create a local configuration from `configs/ai-agent/config.example.json`.
2. Configure the provider endpoint, model, and required credentials locally or through the administration interface.
3. Create a workspace, project, and session, then assign work from the conversation view.

Runtime configuration, generated data, and credentials must remain outside version control. See the documents below for the architecture, API, and security design.

## Documentation

- [Architecture](docs/ai-agent/ARCHITECTURE.md)
- [Development](docs/ai-agent/DEVELOPMENT.md)
- [HTTP API](docs/ai-agent/HTTP_API.md)
- [Security](docs/ai-agent/SECURITY.md)
- [OpenAPI contract](src/transport/httpapi/openapi.yaml)

Release packaging and signing are handled through a maintainer-only workflow. This README does not publish packaging commands, parameters, or signing configuration.

## Data security

- The repository contains sample configuration only. Provider, OAuth, MCP, and NetPass keys, SSH credentials, logs, runtime data, and release artifacts are excluded by `.gitignore`.
- Documentation and examples use relative paths, localhost, or example domains; no developer-specific absolute directory is included.
- Never commit API keys, tokens, private keys, certificates, signing identities, or real service addresses.

## Licensing

This project is dual-licensed:

- Open-source use is available under the [GNU General Public License v3.0 only](LICENSE).
- Commercial licensing is available when GPLv3 obligations are unsuitable; see [COMMERCIAL-LICENSE.md](COMMERCIAL-LICENSE.md).

Third-party components remain subject to their respective bundled licenses.
