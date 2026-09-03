# 쉬지 않는 인턴

[繁體中文](README.md) · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md)

NR-Intern은 Go로 개발된 데스크톱 AI Agent입니다. 현재 대화, 검증된 도구 결과, 저장된 상태를 바탕으로 작업을 계속합니다. 간단한 요청은 바로 처리하고, 긴 작업은 계획을 만들고 단계별로 실행하고 검증할 수 있습니다.

## 주요 기능

- OpenAI-compatible Chat Completions와 ChatGPT／Codex OAuth로 인증하는 OpenAI Codex Responses를 지원하는 확장 가능한 Provider Router.
- 대화별 Provider 및 모델 선택이 가능한 `Workspace → Project → Session` 구조.
- Workspace와 Project의 상시 지침. 작업 규칙을 한 번만 쓰면 이후 모든 대화와 일정에 자동으로 적용됩니다.
- 여러 작업 계획, 드래그 순서 변경, 단계 상태, 도구 결과에 기반한 검증 증거.
- Thinking 단계를 선택할 수 있습니다. 계획을 잠가 순서대로 진행하거나 잠금을 풀어 미완료 계획을 전환할 수 있습니다. 서로 다른 Session은 동시에 작업할 수 있습니다.
- 독립적인 일정 영역. 일정마다 반복 주기와 샌드박스를 지정하고, 시각이 되면 새 대화를 만들어 작업을 시작합니다.
- Durable Run, 재생 가능한 SSE, 스트리밍 응답 및 재연결. UI를 다시 열 때 재연결할 대화를 선택할 수 있으며 중단된 작업은 재시도할 수 있습니다.
- 장애 복구, 선택적으로 사용할 수 있는 영속 알림 센터, Run 일시 중지／재개／전체 취소 제어, 비식별화 진단 패키지.
- 전역 검색, 안전한 백업／복원 및 읽기 전용 권한 센터. 복원 전에 복구 가능한 스냅샷을 자동으로 보존합니다.
- 자격 증명 필드를 가린 설정 패키지를 내려받아 다른 기기에서 Provider, MCP, 리버스 프록시 및 서비스 설정을 참고할 수 있습니다.
- 정보 페이지에서 버전 정보와 업데이트를 확인할 수 있습니다.
- 대화가 실행 중이어도 계속 입력할 수 있으며 후속 메시지는 브라우저 IndexedDB에 저장되어 이전 Run이 끝나면 순서대로 전송됩니다. 재시도 시 동일한 Idempotency-Key를 사용합니다.
- 기록 문자 수 제한을 갖춘 Context 자동 정리, 장기 메모리 및 메모리 범위 제어.
- Run 및 Session별 input／output／total token 사용량을 제공하며, 모델 가격을 설정하면 예상 비용도 표시합니다. 가격이 없으면 token만 표시합니다.
- Sandbox와 실행 승인을 적용한 파일, 문서, Shell, SSH 및 계획 도구.
- 간소 도구 집합에도 문서 읽기, 생성 및 변환이 포함되며 확장 도구, 도구 검색, native／instruction 호출 방식을 선택할 수 있습니다.
- 긴 대화에서 질문을 미리 보고 해당 위치로 이동할 수 있습니다. 누적 사용량은 K／M으로 표시하며 마우스를 올리면 정확한 값을 확인할 수 있습니다.
- 비동기 작업은 취소 가능한 `wait_for`로 대기할 수 있고, 원격 배포는 `ssh_wait`로 읽기 전용 검사를 반복해 bytes, SHA-256 또는 서비스 준비 상태를 확인한 뒤 완료로 판단합니다.
- 외부 자원을 읽는 `http_fetch`. HTML은 자동으로 일반 텍스트가 되고, localhost와 사설 주소는 기본 허용이며, 시스템 설정에서 통째로 끌 수 있습니다.
- 내장 MCP Client로 로컬 stdio, 레거시 SSE 또는 원격 Streamable HTTP Server에 연결하고 외부 도구에도 동일한 권한 및 승인 절차를 적용합니다.
- 선택형 NetPass 리버스 프록시는 데스크톱 제어 UI를 공개하지 않고 백엔드 API만 외부에 제공할 수 있습니다.
- Provider 활성화, 모델 탐색, 도구 권한 및 감사 기록.
- 라이트／다크 화면과 AUTO, 번체 중국어, 영어, 일본어, 한국어 UI.
- Windows x64, Windows ARM64 및 macOS ARM64 데스크톱 환경.
- macOS에서는 시작할 때 상태 표시줄 아이콘을 만듭니다. 작업을 계속 실행한 채 UI를 숨기고 메뉴 또는 앱을 다시 실행해 기존 창을 복원할 수 있습니다.
- Agent로 보내지 않을 메모를 현재 장치에만 저장하는 메모장을 사이드바에서 열 수 있습니다.
- 대화 입력 영역에서 화면 캡처를 시작할 수 있습니다. macOS에서는 시스템 영역 캡처 후 사각형, 직선, 텍스트를 추가하는 편집기를 열며, 복사하거나 닫을 때 편집된 이미지로 클립보드를 갱신합니다. Windows에서는 시스템 캡처 인터페이스를 엽니다.

인터페이스 언어는 UI와 사용자가 변경하지 않은 기본 이름에만 적용됩니다. Agent는 현재 메시지, 최근 대화 및 명시된 선호도를 바탕으로 사용자의 주 사용 언어를 판단합니다.

## 시작하기

1. `configs/ai-agent/config.example.json`을 기준으로 로컬 설정을 만듭니다.
2. Provider endpoint, 모델 및 필요한 인증 정보를 로컬 또는 관리 화면에서 설정합니다.
3. Workspace, Project, Session을 만든 다음 대화 화면에서 작업을 요청합니다.

실제 설정, 실행 데이터 및 인증 정보는 버전 관리에 포함하지 마십시오. 자세한 아키텍처, API 및 보안 설계는 아래 문서를 참조하십시오.

알림은 기본적으로 꺼져 있으며 일반 설정에서 켤 수 있습니다. 설정 패키지는 대화와 첨부를 포함한 전체 백업이 아닙니다. 공유 전 URL, 계정 이름 및 경로를 확인하세요.

## 문서

[문서 웹사이트 열기](https://vaderchen.github.io/NR-Intern/)

- [아키텍처](https://vaderchen.github.io/NR-Intern/architecture.html)
- [문서 도구](https://vaderchen.github.io/NR-Intern/document-tools.html)
- [개발 안내](https://vaderchen.github.io/NR-Intern/development.html)
- [HTTP API](https://vaderchen.github.io/NR-Intern/http-api.html)
- [보안](https://vaderchen.github.io/NR-Intern/security.html)
- [OpenAPI 계약](https://vaderchen.github.io/NR-Intern/openapi.html)

## 데이터 보안

- Repository에는 예제 설정만 포함됩니다. Provider／OAuth／MCP／NetPass 키, SSH 인증 정보, 로그, 실행 데이터 및 릴리스 결과물은 `.gitignore`에서 제외됩니다.
- 문서와 예제는 상대 경로, localhost 또는 example domain만 사용하며 개발자별 절대 디렉터리를 포함하지 않습니다.
- API Key, Token, 개인 키, 인증서, 서명 ID 또는 실제 서비스 주소를 commit하지 마십시오.

## 라이선스

이 프로젝트는 이중 라이선스로 제공됩니다.

- 오픈 소스 사용에는 [GNU General Public License v3.0 only](LICENSE)가 적용됩니다.
- GPLv3 의무가 적합하지 않은 상업적 사용은 별도 협의가 가능하며, [COMMERCIAL-LICENSE.md](COMMERCIAL-LICENSE.md)를 참조하십시오.

타사 구성 요소에는 각각 포함된 라이선스가 적용됩니다.
