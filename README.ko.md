# Tanpopo

[繁體中文](README.md) · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md)

Tanpopo는 Go로 작성된 로컬 모델 서비스 관리자입니다. 이름은 일본어로 민들레를 뜻하는 ‘たんぽぽ’에서 왔으며, 생성된 Token이 민들레 씨앗처럼 퍼져 나간다는 의미를 담고 있습니다. GGUF용 크로스 플랫폼 `llama-server`와 Apple Silicon용 네이티브 Swift/MLX `mlx-server`를 관리합니다.

## 주요 기능

- 하나의 관리 화면에서 모델 Runtime 시작, 중지, 상태 복원 및 로그 확인.
- 하위 폴더를 포함한 GGUF 파일과 완전한 MLX 모델 폴더 자동 탐색.
- Hugging Face의 공개, gated, private repository에서 GGUF 또는 MLX 모델 다운로드.
- Context Size, GPU Layers, Threads, KV Cache, MTP, DFlash 시작 프로필 저장.
- DFlash 지원 여부를 감지하고 활성화 전에 호환되는 Draft 모델 존재 여부 확인.
- Markdown, 수식, reasoning 분리 표시, 대기 애니메이션, Token 수 및 초당 출력 Token 수를 지원하는 임시 로컬 대화.
- MLX가 생성하는 Token을 즉시 전달하는 OpenAI 호환 SSE. 클라이언트 연결 종료 또는 Cancel 시 해당 생성 Task를 바로 취소.
- 모델 API에 접근 키, IP 허용 목록, 둘 다 또는 제한 없음을 선택 가능.
- Tanpopo 재시작 시 관리자 로그인 전에도 Runtime 및 실행 상태 복원.
- 관리 화면에서 `AUTO`, 번체 중국어, 영어, 일본어, 한국어 선택.
- macOS에서 AppKit/WKWebView 네이티브 UI와 시스템 메뉴 막대 상주 모드 제공.

## 빠른 시작

개발에는 Go 1.25 이상, CMake, C/C++ 도구 체인이 필요합니다. Apple Silicon에서 `mlx-server`를 빌드하려면 Swift 6 및 Xcode도 필요합니다.

```bash
cd /path/to/Tanpopo
./run.command
```

최초 실행 시 `agent.sample.properties`에서 `agent.properties`를 만듭니다. 관리 서비스는 기본적으로 `0.0.0.0:10082`에서 수신하며 로컬 주소는 다음과 같습니다.

```text
http://127.0.0.1:10082
```

기본 관리자 로그인 정보:

```text
계정: root
비밀번호: root
```

환경 설정에서 로그인 정보를 변경하거나 확인 대화상자를 거쳐 관리자 로그인을 끌 수 있습니다. 나중에 인증을 다시 켤 수 있도록 기존 로그인 정보는 로컬에 유지됩니다.

macOS에서 상주 모드를 켜면 Tanpopo가 시스템 메뉴 막대에 표시됩니다. 창을 닫아도 UI만 숨겨지며, 서비스를 완전히 중지하려면 메뉴의 **Tanpopo 종료**를 사용하세요. 상주 모드는 기본적으로 꺼져 있습니다.

```bash
TANPOPO_UI=shell ./run.command  # 전경 Shell 모드 강제
TANPOPO_UI=gui ./run.command    # 지원 환경에서 네이티브 UI 강제
```

## 모델 Runtime

### llama-server

`llama-server`는 GGUF와 여러 플랫폼에 대한 높은 호환성을 제공합니다. 기본 GGUF 폴더는 `~/services/models`입니다. 실행할 때 선택한 모델과 저장된 시작 프로필을 결합합니다. 멀티모달 모델은 대응하는 `mmproj`를 선택할 수 있으며 MLX 선택 시 mmproj 항목은 숨겨집니다.

DFlash는 기본적으로 꺼져 있습니다. Tanpopo가 아키텍처와 페어링 정보를 확인하며, 필요한 Draft GGUF가 없으면 스위치를 다시 끄고 다운로드를 안내합니다.

### mlx-server

`mlx-server`는 Swift, SwiftNIO, MLX Swift로 만든 Apple Silicon 전용 Runtime입니다. Python, pip, `mlx_lm.server`를 호출하지 않습니다. 기본 MLX 모델 폴더는 `~/services/mlx-models`이며 유효한 모델에는 `config.json`과 safetensors 가중치가 있어야 합니다.

주요 호환 엔드포인트:

```text
GET  /health
GET  /v1/health
GET  /models
GET  /v1/models
POST /v1/chat/completions
POST /v1/completions
POST /completion
```

Runtime 상태에 표시되는 Base URL(보통 `http://127.0.0.1:8080/v1`)을 사용하세요. `/models`와 `/v1/models`는 현재 불러온 정확한 Model ID를 반환합니다.

`/v1/chat/completions`에 `stream: true`를 지정하면 생성 중인 Token을 OpenAI 호환 SSE로 즉시 전송합니다. HTTP Channel이 닫히면 producer Task와 MLX 생성 스트림도 취소됩니다.

## API 보안

접근 키와 IP 허용 목록을 각각 켤 수 있습니다. 둘 다 켜면 두 검사를 모두 통과해야 합니다. 키는 Bearer 또는 `X-OpenLoader-Key`로 전송할 수 있습니다. 키 원문은 발급 시 한 번만 표시되고 SHA-256 해시만 저장됩니다.

IP 허용 목록은 IPv4, IPv6, CIDR, 와일드카드 접미사 및 `*`를 지원합니다. 활성화된 정책 스냅샷이 없거나 손상된 경우 Runtime은 fail-closed로 요청을 거부합니다.

## 로컬 데이터

- `data/settings.json`: 일반 설정 및 인터페이스 언어. 원자적으로 저장됩니다.
- `data/runtime_state.json`: Runtime, 모델, 시작 프로필 및 원하는 실행 상태.
- `agent.properties`: 관리자 로그인 설정.
- API 보안 파일: 정책 및 접근 키 해시.

대화 내용은 저장하지 않습니다.

## 빌드

```bash
go build -buildvcs=false -trimpath -o bin/Tanpopo ./src/cmd/llamaloader
./scripts/build-mlx-server-runtime.sh  # Apple Silicon 전용
```

## 라이선스

[LICENSE](LICENSE), [COMMERCIAL-LICENSE.md](COMMERCIAL-LICENSE.md), [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)를 확인하세요.
