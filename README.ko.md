# 쉬지 않는 인턴

[繁體中文](README.md) · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md)

NR-Intern은 Go로 개발된 데스크톱 AI Agent입니다. 현재 대화, 검증된 도구 결과, 저장된 상태를 바탕으로 작업을 계속합니다. 간단한 요청은 바로 처리하고, 긴 작업은 계획을 만들고 단계별로 실행하고 검증할 수 있습니다.

## 주요 기능

- 원격 및 로컬 모델 서비스를 지원하는 확장 가능한 OpenAI-compatible Provider Router.
- 대화별 Provider 및 모델 선택이 가능한 `Workspace → Project → Session` 구조.
- 여러 작업 계획, 드래그 순서 변경, 단계 상태, 도구 결과에 기반한 검증 증거.
- Durable Run, 재생 가능한 SSE, 스트리밍 응답 및 재연결.
- Context 자동 정리, Session 간 장기 메모리 및 메모리 범위 제어.
- Sandbox와 실행 승인을 적용한 파일, 문서, Shell, SSH 및 계획 도구.
- Provider 활성화, 모델 탐색, 도구 권한 및 감사 기록.
- 라이트／다크 화면과 AUTO, 번체 중국어, 영어, 일본어, 한국어 UI.
- Windows x64, Windows ARM64 및 macOS ARM64 데스크톱 환경.

인터페이스 언어는 UI와 사용자가 변경하지 않은 기본 이름에만 적용됩니다. Agent는 현재 메시지, 최근 대화 및 명시된 선호도를 바탕으로 사용자의 주 사용 언어를 판단합니다.

## 시작하기

1. `configs/ai-agent/config.example.json`을 기준으로 로컬 설정을 만듭니다.
2. Provider endpoint, 모델 및 필요한 인증 정보를 로컬 또는 관리 화면에서 설정합니다.
3. Workspace, Project, Session을 만든 다음 대화 화면에서 작업을 요청합니다.

실제 설정, 실행 데이터 및 인증 정보는 버전 관리에 포함하지 마십시오. 자세한 아키텍처, API 및 보안 설계는 아래 문서를 참조하십시오.

## 문서

- [아키텍처](docs/ai-agent/ARCHITECTURE.md)
- [개발 안내](docs/ai-agent/DEVELOPMENT.md)
- [HTTP API](docs/ai-agent/HTTP_API.md)
- [보안](docs/ai-agent/SECURITY.md)
- [OpenAPI 계약](src/transport/httpapi/openapi.yaml)

릴리스 패키징과 서명은 관리자 전용 내부 절차로 처리합니다. 이 README에는 패키징 명령, 매개변수 또는 서명 설정을 공개하지 않습니다.

## 데이터 보안

- Repository에는 예제 설정만 포함됩니다. Provider 키, SSH 인증 정보, 로그, 실행 데이터 및 릴리스 결과물은 `.gitignore`에서 제외됩니다.
- 문서와 예제는 상대 경로, localhost 또는 example domain만 사용하며 개발자별 절대 디렉터리를 포함하지 않습니다.
- API Key, Token, 개인 키, 인증서, 서명 ID 또는 실제 서비스 주소를 commit하지 마십시오.

## 라이선스

이 프로젝트는 이중 라이선스로 제공됩니다.

- 오픈 소스 사용에는 [GNU General Public License v3.0 only](LICENSE)가 적용됩니다.
- GPLv3 의무가 적합하지 않은 상업적 사용은 별도 협의가 가능하며, [COMMERCIAL-LICENSE.md](COMMERCIAL-LICENSE.md)를 참조하십시오.

타사 구성 요소에는 각각 포함된 라이선스가 적용됩니다.
