# Tanpopo

[繁體中文](README.md) · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md)

Tanpopo는 Go로 작성된 로컬 모델 서비스 관리자입니다. 이름은 일본어로 민들레를 뜻하는 ‘たんぽぽ’에서 왔으며, 생성된 Token이 민들레 씨앗처럼 퍼져 나간다는 의미를 담고 있습니다. GGUF용 크로스 플랫폼 `llama-server`와 Apple Silicon용 네이티브 Swift/MLX `mlx-server`를 관리합니다.

## 주요 기능

- 하나의 관리 화면에서 모델 Runtime 시작, 중지, 상태 복원 및 로그 확인.
- 하위 폴더를 포함한 GGUF 파일과 완전한 MLX 모델 폴더 자동 탐색. Apple Silicon의 `mlx-server`는 safetensors로 미리 변환하지 않고 지원되는 GGUF를 직접 불러올 수 있습니다.
- **Fast GGUF 모드**는 `mlx-server`에서 GGUF를 선택할 때 기본으로 켜지며, 모델 이름에 의존하지 않는 tensor 규칙으로 INT4, INT8, BF16, group size를 선택하고 변환된 가중치를 `.fgguf`로 영구 저장합니다. 시스템 설정에서 기본, Beta 1, Beta 2의 세 가지 빠른 전략을 선택할 수 있습니다. mlx-server가 해석할 수 있는 대부분의 GGUF에 도움이 되지만 모든 아키텍처, 양자화 형식, 사용자 정의 checkpoint의 로드, 가속 또는 동일한 정밀도를 보장하지는 않습니다.
- mlx-server 모델을 GGUF와 MLX로 구분합니다. Runtime이 아직 호환성을 명시하지 않은 언어 모델도 선택 가능한 **미테스트(N)** 그룹에 남기고, 시작 시 실제 로드로 호환성을 확인합니다.
- Hugging Face의 공개, gated, private repository에서 GGUF 또는 MLX 모델 다운로드.
- 별도 JSON 목록에서 자주 사용하는 GGUF 및 MLX 모델을 빠르게 선택합니다. 각 형식은 8B급, 30B급, 70B 이상으로 분류하고 그룹 안에서는 모델 이름을 알파벳순으로 표시합니다. Runtime, repository, revision, GGUF 파일 이름을 자동 입력하며 모델 항목은 JavaScript에 하드코딩하지 않습니다.
- 서버가 Range를 지원하면 큰 파일을 64 MiB 단위, 최대 4개 Worker로 병렬 다운로드하고, 지원하지 않으면 단일 Stream으로 자동 전환합니다. 완료 항목은 자동으로 지워집니다. 저장 위치 열기는 Desktop App에서만 활성화되고 Browser에서는 비활성화됩니다.
- Context Size, GPU Layers, Threads, KV Cache, MTP, DFlash 시작 프로필 저장.
- 서로 독립적인 DFlash와 MMap 스위치를 간결한 ‘고급 설정’ 팝오버에 모아, 항목이 늘어도 페이지가 계속 길어지지 않도록 구성.
- DFlash 지원 여부를 감지하고 활성화 전에 호환되는 Draft 모델 존재 여부 확인. 별도의 MMap 스위치는 llama-server와 Apple Silicon mlx-server를 모두 지원하며 파일 기반 페이지로 모델 로딩 시 메모리 부담을 줄입니다.
- KV Cache, MMap, DFlash는 기능 기본값, 시작 Profile, 실행 스위치, 호환성 사전 검사, Runtime 인자, 상태 저장, 오류 보고까지 전체 과정에 통합됩니다. 실제 사용 가능 여부는 Runtime과 모델에 따라 달라지며, DFlash에는 호환 Target/Draft가 필요하고 KV Cache 양자화와 동시에 사용할 수 없습니다.
- 고정된 모델 목록 대신 Hugging Face metadata와 모델 설정을 검증하여 같은 repository 또는 별도 repository의 DFlash Draft를 자동 검색 및 다운로드.
- Markdown, 수식, reasoning API 필드, `<think>`, `<|channel|>` 사고 채널 분리 표시를 지원하는 임시 로컬 대화. 사고 과정은 생성 중 기본으로 펼쳐지고 점 세 개 애니메이션을 표시한 뒤 생성이 끝나면 자동으로 접힙니다. Token 수와 초당 출력 Token 수도 표시합니다.
- 모델 시작 시 대형 모델은 변환이 필요할 수 있음을 알리는 로딩 대화 상자를 즉시 표시합니다. 실행 중인 모델 테스트에는 애니메이션 대화 상자를 사용하고, 완료 후 입력/출력 Token, 생성 속도, 경과 시간 또는 로딩/연결 오류를 보여 줍니다. 메인 화면 새로 고침에는 별도의 작은 진행 대화 상자를 사용합니다.
- MLX가 생성하는 Token을 즉시 전달하는 OpenAI 호환 SSE. 클라이언트 연결 종료 또는 Cancel 시 해당 생성 Task를 바로 취소.
- 모델 API에 접근 키, IP 허용 목록, 둘 다 또는 제한 없음을 선택 가능.
- Tanpopo 재시작 시 관리자 로그인 전에도 Runtime 및 실행 상태 복원.
- 관리 화면에서 `AUTO`, 번체 중국어, 영어, 일본어, 한국어 선택.
- 민들레, 맑은 하늘 파랑, 벚꽃 분홍, 어두운 Midnight Purple의 4가지 테마 제공.
- 하단 상태 표시줄에서 CPU, GPU, MEMORY, 네트워크 상태를 3초마다 갱신하고 50%/80%를 기준으로 저채도 녹색, 노란색, 빨간색으로 표시.
- **시스템 설정 → 시스템 정보**에서 OS, Kernel, Architecture, Host 이름, CPU, GPU, Memory, Network interface 및 접근 가능한 관리 URL을 읽기 전용으로 표시. Loopback URL은 공유 URL 목록에서 제외합니다.
- 시작 시와 매시간 GitHub의 최신 정식 Release를 확인하고, 새 버전이 있으면 알림 표시. **시스템 설정 → 정보**에서 현재 버전, 수동 업데이트 확인 및 클릭 가능한 GitHub URL 제공.
- macOS에서 AppKit/WKWebView 네이티브 UI와 시스템 메뉴 막대 상주 모드 제공.

## 공개 테스트 보고서

- [모델 호환성 보고서](https://vaderchen.github.io/Tanpopo/reports/model-compatibility.html): 네이티브 MLX, MLX의 GGUF 로드, llama.cpp GGUF, 멀티모달 프로젝션, KV Cache 양자화 및 추측 디코딩의 지원 범위와 호환성 경계를 정리합니다.
- [MLX 및 GGUF 변환의 로딩 속도와 연산 정확도](https://vaderchen.github.io/Tanpopo/reports/performance-comparison.html): 같은 모델에서 변환하지 않은 네이티브 MLX, MLX + 기본 빠른 변환, MLX + Beta 1, MLX + Beta 2를 비교하고 고정 100문항 결과, 생성 속도, 원본 모델과 변환 캐시 용량, 프로세스 RAM 최고값과 평균값을 그대로 공개합니다.

두 HTML 보고서는 `AUTO`, 번체 중국어, 영어를 전환할 수 있습니다. 결과는 명시된 날짜, 하드웨어, Runtime 버전 및 샘플에서 재현 가능한 스냅샷이며, 모든 모델이나 장치에서 같은 결과를 보장하거나 Fast GGUF의 호환성, 속도 또는 정확도를 보장하지 않습니다.

## 빠른 시작

개발에는 Go 1.25 이상, CMake, C/C++ 도구 체인이 필요합니다. Apple Silicon에서 `mlx-server`를 빌드하려면 Swift 6 및 Xcode도 필요합니다.

```bash
cd /path/to/Tanpopo
./run.command
```

`run.command`는 먼저 `build.command --runtime`을 호출합니다. 고정 버전이 일치하고 Runtime 소스가 prebuilt 실행 파일보다 새롭지 않을 때만 기존 Runtime을 재사용하며, 그 외에는 자동으로 다시 빌드합니다.

최초 실행 시 `agent.sample.properties`에서 `agent.properties`를 만듭니다. 관리 서비스는 기본적으로 `0.0.0.0:10082`에서 수신하며 로컬 주소는 다음과 같습니다.

```text
http://127.0.0.1:10082
```

초기 로컬 로그인 정보는 `agent.sample.properties`에서 생성되며 첫 실행 용도입니다. LAN 접근 또는 Reverse Proxy를 활성화하기 전에 반드시 변경하세요. 시스템 설정에서 로그인 정보를 바꾸거나 경고를 확인한 뒤 관리자 로그인을 끌 수 있으며, 나중에 인증을 다시 켤 수 있도록 정보는 로컬에만 유지됩니다.

macOS에서 상주 모드를 켜면 Tanpopo가 시스템 메뉴 막대에 표시됩니다. 창을 닫아도 UI만 숨겨지며, 서비스를 완전히 중지하려면 메뉴의 **Tanpopo 종료**를 사용하세요. 상주 모드는 기본적으로 꺼져 있습니다.

```bash
TANPOPO_UI=shell ./run.command  # 전경 Shell 모드 강제
TANPOPO_UI=gui ./run.command    # 지원 환경에서 네이티브 UI 강제
```

앱 버전은 `1.YY.MMDD build HHmm` 형식을 사용합니다. `run.command`, `run.sh`, `build.command`, `pack.command`는 모두 현재 `Asia/Taipei` 날짜에서 `1.YY.MMDD`를, 실행 시각에서 `build HHmm`을 직접 생성합니다. 루트 `VERSION`을 읽거나 검증하거나 수정하지 않으므로 날짜가 바뀐 뒤 실행하거나 다시 패키징해도 수동으로 날짜를 고칠 필요가 없습니다. 과거 릴리스를 의도적으로 다시 만들 때만 `TANPOPO_VERSION`을 지정하고, Build 번호를 고정해야 할 때는 `TANPOPO_BUILD`를 사용하십시오. 생성된 패키지에는 실제 적용된 버전을 기록합니다. 루트 `VERSION`은 버전 metadata와 이전 흐름 호환 용도로만 유지됩니다. Tanpopo는 시작 직후와 이후 매시간 GitHub의 최신 정식 Release를 확인하고, 같은 UI 세션에서는 같은 새 버전을 한 번만 알립니다. **시스템 설정 → 정보**에서 현재 및 최신 버전, 마지막 확인 시각, 수동 확인 버튼, 클릭 가능한 [GitHub 저장소 URL](https://github.com/VaderChen/Tanpopo)을 볼 수 있습니다. 업데이트 확인은 `1.YY.MMDD`만 비교하며 Draft와 prerelease는 최신 버전으로 취급하지 않습니다.

## 모델 Runtime

### llama-server

`llama-server`는 GGUF와 여러 플랫폼에 대한 높은 호환성을 제공합니다. 기본 GGUF 폴더는 `~/services/models`입니다. 실행할 때 선택한 모델과 저장된 시작 프로필을 결합합니다. 멀티모달 모델은 대응하는 `mmproj`를 선택할 수 있습니다.

DFlash는 기본적으로 꺼져 있습니다. Tanpopo가 아키텍처와 페어링 정보를 확인하며, 필요한 Draft GGUF가 없으면 스위치를 다시 끄고 다운로드를 안내합니다.

MMap은 실행 상태 페이지의 ‘고급 설정’ 팝오버에 있는 독립 스위치이며 기본값은 꺼짐입니다. `llama-server`와 Apple Silicon `mlx-server`를 모두 지원합니다. llama-server는 `--load-mode mmap`을 사용하고, mlx-server는 지원되는 safetensors 및 직접 사용할 수 있는 GGUF 가중치를 파일 기반 페이지로 매핑합니다. 시작 프로필에서 자동 또는 4, 8, 16, 24, 32, 48, 64, 96, 128 GB의 메모리 예약 목표를 선택할 수 있습니다. llama-server는 `--fit-target`으로 GPU Layers를 구성하고, mlx-server는 물리 메모리에서 예약 목표를 뺀 값을 MLX 할당 목표로 사용합니다. 이 값은 Runtime의 엄격한 메모리 사용 한도가 아닙니다. 첫 Token 지연 시간과 생성 속도는 저장 장치 성능 및 페이지 부하에 따라 달라집니다.

시작 프로필에서 KV Cache Q8 또는 Q4를 선택하고 ‘고급 설정’의 별도 스위치로 이번 실행에 적용할지 결정합니다. 스위치를 끄면 양자화하지 않습니다. Q4는 메모리를 더 절약하고 Q8은 더 높은 정밀도를 유지합니다. KV Cache 양자화와 DFlash는 UI와 백엔드에서 상호 배타적입니다.

### mlx-server

`mlx-server`는 Swift, SwiftNIO, MLX Swift로 만든 Apple Silicon 전용 Runtime입니다. Python, pip, `mlx_lm.server`를 호출하지 않습니다. `~/services/mlx-models`의 완전한 MLX 모델과 일반 GGUF 폴더의 지원되는 GGUF를 직접 불러올 수 있습니다. 네이티브 MLX 지원 형식은 번들된 `mlx-swift-lm 3.31.4` Registry에서 동적으로 가져오며 멀티모달 Gemma 4를 포함합니다. Runtime이 현재 보고하는 GGUF 직접 로딩 대상은 Gemma, Llama, Mimo, MiniCPM, Mistral, Qwen 2, Qwen 3, Qwen 3.5, SmolLM3이며, 탐지된 미지원 언어 모델은 비활성 상태로 표시합니다. 멀티모달 Qwen 3.5 GGUF는 대응하는 `mmproj`를 선택할 수 있습니다.

**Fast GGUF 모드**는 mlx-server의 범용 GGUF 최적화 진입점이며 GGUF를 선택하면 기본으로 켜집니다. 시스템 설정의 별도 **Fast GGUF 전략** 카드에서 기본 스위치와 세 가지 전략을 저장합니다.

- **기본**: `speed + group auto + recurrent controls`. auto는 Group 64로 결정되고 K-Quant는 INT8로 재양자화됩니다.
- **Beta 1**: `speed-passthrough + group 32 + recurrent controls`. 표현 가능한 Q4_K 원본 4-bit sub-block을 재사용하고 나머지 tensor는 Group 32를 사용합니다.
- **Beta 2**: `speed-passthrough + group 64 + recurrent controls`. Q4_K 재사용은 Beta 1과 같고 나머지 tensor는 Group 64를 사용합니다.

Fast GGUF를 끄면 일반 `auto + group auto + recurrent off` 변환을 사용합니다. 모든 전략은 모델 이름이 아니라 tensor dtype, shape, source block, architecture metadata로 결정됩니다. 재사용된 Q4_K의 32요소 Group은 원본 sub-block 형식이 정의한 것이며 전체 모델에 Group 32를 적용한다는 뜻이 아닙니다. `quality`는 FP32 참조 가중치를 사용하는 진단용 모드이며 일반 성능 모드가 아닙니다.

이 전략은 모델별 예외 없이 mlx-server가 해석할 수 있는 대부분의 GGUF에 적용되지만 호환성, 속도 또는 정밀도를 보장하지는 않습니다. 아키텍처 규약, GGUF metadata, Tokenizer, tensor layout, 양자화 방식 및 사용자 정의 변경에 따라 로드 실패, 속도 향상 없음 또는 품질 차이가 생길 수 있습니다. 이상이 있으면 Fast GGUF를 끄고 비교한 뒤 실제 생성 품질을 검증하십시오. GGUF Target은 일반 MLX 생성을 사용하며 DFlash는 MLX safetensors Target과 호환 Draft 조합으로 제한됩니다.

mlx-server에서 KV Cache 양자화를 켜면 프로필의 Q8 또는 Q4, group size 64, 2,048 Token 이후의 지연 양자화를 사용합니다. 프로필 Context Size가 양자화 KV Cache 한도가 됩니다.

KV Cache, MMap, DFlash는 숨겨진 수동 플래그가 아니라 Tanpopo의 일급 기능입니다. 시스템 설정에서 모델 기능 기본값을 저장하고, 시작 Profile에서 세부 값을 정하며, 실행 화면에서 이번 로드에 한해 재정의할 수 있습니다. Backend는 호환성과 상호 배타 조건을 검사하고 Runtime 인자 구성, 상태 저장, 오류 표시까지 처리합니다. 다만 모든 모델이 모든 기능을 지원한다는 뜻은 아닙니다. KV Cache 형식과 MMap 동작은 Runtime에 따라 달라지고 DFlash에는 호환 Target/Draft가 필요합니다.

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

## NetPass Reverse Proxy

NetPass는 관리자 로그인 인증과 Model API Access Key 인증이 모두 켜져 있을 때만 시작할 수 있습니다. 사용자가 활성화를 요청할 때 조건을 검사하고 다국어 사용 정책 및 책임 설명을 표시하며, 내용을 읽었다는 명시적인 확인 이후에만 Reverse Proxy를 시작합니다. 연결되면 할당된 공개 NetPass URL을 보여 주며, 로컬 관리 화면과 API 호출이 공용 네트워크에서 접근 가능해집니다.

NetPass는 Tanpopo와 Mars Semi Corp.의 기술 협력으로 제공되는 기술 교류 및 실험 목적의 서비스입니다. 현재는 무료로 제공되지만 Mars Semi Corp.는 언제든 사용 정책을 변경할 수 있으며 주요 변경은 별도로 공지합니다. 사용자는 필요성을 판단하고 적절한 보안 설정을 적용하며 네트워크 보안 위험을 부담해야 합니다. 서비스 사용으로 발생한 보안 사고, 데이터 노출 또는 기타 손실에 대해 Mars Semi Corp.와 Tanpopo는 책임을 지지 않습니다.

NetPassClient는 별도의 Closed-source component입니다. Source, Binary, Credential 및 공식 Packaging process는 이 Repository에 포함되지 않으며 GitHub에 동기화되지 않습니다. 공식 서명 Installer에는 관리되는 Platform-specific binary가 포함될 수 있습니다. Server API Key는 권한이 제한된 로컬 설정에만 저장되고 Management API는 평문을 반환하지 않습니다.

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

[LICENSE](LICENSE), [COMMERCIAL-LICENSE.md](COMMERCIAL-LICENSE.md), [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)를 확인하세요. 보안 제보 및 로컬 비밀 정보 처리 지침은 [SECURITY.md](SECURITY.md)에 있습니다.
