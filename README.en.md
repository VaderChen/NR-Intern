# Tireless Intern

[繁體中文](README.md) · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md)

NR-Intern is a desktop AI agent written in Go. It keeps working from the current conversation, verified tool results, and persisted state. Simple requests are handled directly, while longer tasks can be planned, executed step by step, and verified.

## Highlights

- Extensible OpenAI-compatible provider routing for remote and local model services.
- A `Workspace → Project → Session` hierarchy with per-conversation provider and model selection.
- Multiple ordered work plans with drag-and-drop ordering, step states, and tool-backed evidence.
- Durable runs, replayable SSE, streamed responses, and reconnection support.
- Automatic context compaction, cross-session memory, and configurable memory scopes.
- Native file, document, shell, SSH, and planning tools protected by sandboxes and execution approval.
- Provider enablement, model discovery, tool permissions, and audit records.
- Light and dark appearance with Auto, Traditional Chinese, English, Japanese, and Korean interfaces.
- Desktop support for Windows x64, Windows ARM64, and macOS ARM64.

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

- The repository contains sample configuration only. Provider keys, SSH credentials, logs, runtime data, and release artifacts are excluded by `.gitignore`.
- Documentation and examples use relative paths, localhost, or example domains; no developer-specific absolute directory is included.
- Never commit API keys, tokens, private keys, certificates, signing identities, or real service addresses.

## Licensing

This project is dual-licensed:

- Open-source use is available under the [GNU General Public License v3.0 only](LICENSE).
- Commercial licensing is available when GPLv3 obligations are unsuitable; see [COMMERCIAL-LICENSE.md](COMMERCIAL-LICENSE.md).

Third-party components remain subject to their respective bundled licenses.
