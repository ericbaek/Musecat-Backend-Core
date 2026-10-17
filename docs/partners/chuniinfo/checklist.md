# CHUNITHM Info 측 할 일 체크리스트

작성 기준: 2026-10-18. 2026-10-15에 전달한 `agent.md`, `developer-api.md`, `developer-openapi.yaml`,
`joint-connections.md`의 **보완 문서**다. 설명은 그 문서들에 있고, 여기에는 츄니인포 측이
**결정·제공·구현·테스트**해야 하는 항목만 모았다. 각 항목 끝의 `[ ]`를 채워 돌려주면 된다.

Musecat 측 준비 상태(2026-10-18 기준)와 10/15 이후 변경분은 마지막 절에 있다.

---

## A. 결정해서 알려줄 것 (양측 합의가 필요한 사항)

양방향 원클릭 연결을 켜려면 아래가 먼저 확정돼야 한다. 확정 전까지 Musecat은 공동 연결 공개 API를 열지 않는다.

| # | 결정 항목 | 선택지 / 기준 | 답 |
| --- | --- | --- | --- |
| A1 | **공동 동의 화면 권한 위임**: Musecat 로그인형 동의 화면 한 번으로 "츄니인포가 뮤즈캣 일반 기능 사용" + "뮤즈캣이 츄니인포 전적 조회" 두 권한을 함께 수집하는 데 동의하는가 | 동의 / 거부(각자 동의 화면 유지) | [ ] |
| A2 | **consent manifest 버전 체계**: 문구·목적·scope·게임 서비스·공개 정책을 고정한 버전 ID와 변경 시 재동의 규칙 | 예: `chuniinfo-musecat/v1` | [ ] |
| A3 | **intent 교환 방식**: 츄니인포가 로그인 사용자에게 발급하는 승인 전 intent를 Musecat 서버가 어떻게 검증할지 | (권장) opaque handle + 서버 간 인증 조회 / 서명 토큰(알고리즘·키 배포·회전 합의 필요) | [ ] |
| A4 | **receipt 교환 방식**: Musecat 승인 후 츄니인포가 그 승인을 확인하고 자기 code를 발급하는 서버 간 확인 절차 | (권장) 일회성 opaque receipt를 인증된 서버 호출로 상환 | [ ] |
| A5 | **서버 간 인증**: intent/receipt/revoke 엔드포인트용 인증 방식. 기존 OAuth client secret 재사용은 권장하지 않음 | 별도 purpose/audience를 가진 credential 또는 mTLS | [ ] |
| A6 | **수명·일회성**: intent/receipt 최대 수명(≤ 5분 제안), 원자적 1회 소비, 재사용 시 동작 | | [ ] |
| A7 | **양방향 해제 계약**: pair ID + generation 기준 revoke 알림, 중복/순서 역전/이전 세대 메시지 무시 규칙, 재시도 정책 | | [ ] |
| A8 | **부분 연결 상태 UX**: 한쪽만 완료됐을 때 각 서비스가 사용자에게 보여줄 상태와 재시도/중단 동선 | | [ ] |
| A9 | **전적 공개 범위**: 역방향으로 Musecat이 받을 수 있는 데이터 범위와 사용자 비공개 설정 반영 방식 | 아래 B 섹션의 필드 표 참조 | [ ] |
| A10 | **rhythm.info 전환 계획**: issuer/client/subject가 바뀔 때 기존 연결을 어떻게 처리할지 | 재연결 요구 / 검증된 subject 매핑 제공 | [ ] |

---

## B. 제공할 값 (Musecat 설정에 그대로 들어가는 것)

### B1. 단방향 (츄니인포 → 뮤즈캣 일반 기능) — Musecat OAuth client 등록용

| 항목 | 요구 사항 | 값 |
| --- | --- | --- |
| 앱 표시 이름 / 설명 | 동의 화면에 그대로 노출. 설명은 "관리 기능 제외" 취지 포함 권장 | [ ] |
| 개발용 callback URL | 정확한 HTTPS URL, query/fragment 없음. localhost 불가 | [ ] |
| 운영용 callback URL | 위와 동일 조건, 개발용과 분리 | [ ] |
| 요청 scope | `account:access` 하나로 확정 (기존 6개 좁은 scope 사용 안 함) | [ ] |

→ Musecat이 `client_id`와 32자 이상 `client_secret`을 비공개 채널로 회신한다. 변경 시 기존 grant는 모두 무효화되므로 callback 변경은 미리 알린다.

### B2. 역방향 (뮤즈캣 → 츄니인포 전적) — Musecat `MUSECAT_EXTERNAL_PROVIDERS` 등록용

Musecat의 역방향 어댑터는 **일반 OAuth 2.0 Authorization Code + PKCE S256 클라이언트**다. OIDC ID token은 쓰지 않는다. 아래 값을 모두 채워야 provider를 등록할 수 있다.

| 항목 | Musecat 어댑터 요구 사항 | 값 |
| --- | --- | --- |
| `issuer` | 고정 HTTPS origin. authorization 응답과 userinfo에 `iss`가 있으면 이 값과 정확히 일치해야 함 | [ ] |
| `authorization_url` | 고정 HTTPS. query/fragment/userinfo 없음 | [ ] |
| `token_url` | 위와 동일. 리다이렉트 응답 거부, 10초 총 deadline | [ ] |
| `userinfo_url` | 위와 동일. `Authorization: Bearer` GET, JSON 응답 ≤ 64 KiB | [ ] |
| `subject_field` | userinfo JSON **최상위**의 **문자열** 필드명 (예: `"sub"`). 숫자/중첩 객체는 현재 어댑터가 거부 → 변경 필요하면 미리 알릴 것 | [ ] |
| `client_auth_method` | `client_secret_basic` 또는 `client_secret_post` 중 하나. 자동 재시도 없음 | [ ] |
| `require_issuer` | authorization 응답에 `iss` 파라미터(RFC 9207)를 항상 보내는지 | 예 / 아니오 [ ] |
| `scopes` (identity) | 계정 식별에 필수인 scope 목록 | [ ] |
| `services[]` | 게임 서비스별 `{id, name, scopes}`. 예: `chunithm` ← `chuni:read` 등 **실제 scope 이름** | [ ] |
| Musecat callback | Musecat이 제시하는 callback을 **정확히** 등록. 개발/운영 분리 | Musecat이 전달 |
| Musecat client credential | 츄니인포가 발급해 비공개 채널로 전달 | [ ] |
| PKCE S256 지원 여부 | 필수 | 예 / 아니오 [ ] |
| token 응답 | `access_token`, `token_type`, `expires_in`, 가능하면 `refresh_token`, `scope`(생략 시 요청 scope 전부 승인으로 간주) | [ ] |
| refresh 정책 | 회전 여부, 절대 수명. **refresh token 없이 만료된 access token은 공동 연결 증거로 인정되지 않음** | [ ] |
| revocation endpoint | 현재 Musecat은 해제 시 호출하지 않음(로컬 해제만). 향후 호출을 원하면 endpoint·인증 방식 제공 | [ ] |
| 전적 조회 API | endpoint, 응답 스키마, 페이지네이션, 비공개 시 응답(404/빈 값/필드별 마스킹), rate limit | [ ] |

전적 필드 후보(확약 아님, 가능 여부 표시):

| 필드 | 제공 가능 | 비공개 설정 영향 |
| --- | --- | --- |
| 현재 레이팅 / 최고 레이팅 | [ ] | [ ] |
| OVER POWER | [ ] | [ ] |
| 칭호 · 레벨 · 팀 | [ ] | [ ] |
| 플레이 보면 수 / FC·AJ 수 / 랭크 분포 | [ ] | [ ] |
| 마지막 플레이 · 동기화 시각 | [ ] | [ ] |
| 베스트 곡 목록 (범위/개수) | [ ] | [ ] |
| 최근 플레이 이력 (범위/페이지) | [ ] | [ ] |
| 외부 링크(프로필 URL) | [ ] | [ ] |

### B3. 공동 연결 (A에서 합의된 후)

| 항목 | 값 |
| --- | --- |
| intent 발급/조회 endpoint, 요청·응답 스키마, 에러 코드 | [ ] |
| receipt 확인 endpoint, 요청·응답 스키마, 에러 코드 | [ ] |
| revoke 알림 수신 endpoint, 멱등성 키(pair ID + generation) | [ ] |
| 서버 간 인증 credential / 키 | [ ] |
| 지원 manifest 버전 목록 | [ ] |

---

## C. 구현할 것

### C1. 지금 바로 가능 (Musecat 단방향 계약은 확정됨)

- [ ] 설정 주입: `MUSECAT_OAUTH_ISSUER`, `MUSECAT_CLIENT_ID`, `MUSECAT_CLIENT_SECRET`, `MUSECAT_REDIRECT_URI`. 미설정이면 연결 버튼 비활성/안내. 개발·운영 분리.
- [ ] issuer 수신 후 `/.well-known/oauth-authorization-server`에서 endpoint 읽기, `issuer` 일치 검증.
- [ ] 연결 시작: 세션 바인딩 state, PKCE verifier(43–128자)+S256, `scope=account:access`.
- [ ] callback: 세션·state·`iss` 검증, 중복 파라미터/`code`+`error` 동시 전달 거부, `access_denied` 정상 처리.
- [ ] token 교환(Basic, form-urlencoded), 90초 code 1회 사용, scope에 `account:access` 포함 확인.
- [ ] `GET /developer/v1/me`로 `sub` 확인 → `(issuer, client registration, sub)`로 저장. 다른 츄니인포 계정에 이미 연결된 identity는 충돌 처리(자동 이전 금지).
- [ ] token 저장 암호화, 브라우저에는 츄니인포 세션 쿠키만. 모든 Musecat API 호출은 서버/BFF에서.
- [ ] refresh: 연결별 직렬화, access/refresh 원자 교체, 30일 family 만료 처리, 재사용 금지.
- [ ] revoke: `POST /oauth/revoke` + 로컬 credential/진행 중 요청 폐기. 원격 실패는 사용자에게 구분 표시.
- [ ] 401/`invalid_grant` → 재연결 유도, 403 → 기능 오류 표시(재연결 강요 금지), 429 → 대기(변경 요청 무조건 재전송 금지).
- [ ] 일반 기능 연동: `/developer/v1/account/*` 83개 operation 중 1차 범위 — 조회(`GET .../arcades`, `.../arcade`), 수정 1개(`PUT .../arcade/basic` 등), 고장 신고(`POST .../arcade/flag`), 사진 업로드→적용(`POST .../arcade/photo/upload` → `PUT .../arcade/photo`).
- [ ] 위치가 필요한 기능은 사용자 동의 후 실제 좌표 전달 (연결 ≠ 위치 인증).
- [ ] 연결 관리 UI: 연결 상태, 해제, 재연결, "Musecat 설정에서도 해제 가능" 안내.

### C2. 역방향 provider 준비 (B2 값과 함께)

- [ ] Musecat을 confidential client로 등록 (callback 정확 등록, PKCE S256, 선택한 client auth 방식).
- [ ] userinfo에 안정적 문자열 `subject_field` 노출 (계정 통합 후에도 유지 가능해야 함).
- [ ] 서비스별 scope로 전적 접근 분리 (`chunithm` 등). 비공개 설정이 API 응답에 반영되는지 확인.
- [ ] (가능하면) refresh token 발급과 revocation endpoint.

### C3. 공동 연결 어댑터 (A 합의 후에만)

- [ ] session-bound intent 발급 + 저장(해시만), 5분 만료, 1회 소비.
- [ ] Musecat 서버의 intent 조회 요청 인증·응답.
- [ ] receipt 확인 후에만 Musecat용 OAuth code 발급. bool/UID/미검증 서명으로 발급 금지.
- [ ] 양쪽 완료 acknowledgment 멱등 처리, code/refresh 교환은 무조건 재시도 금지.
- [ ] 두 계정 표시 + 공동 권한 요약 + 단일 승인 + partial/expired/retry UI (어느 쪽에서 시작하든).
- [ ] 양방향 해제: 로컬 즉시 차단 → 원격 revoke 알림 durable 전송, pair/generation 검증, 이전 세대 메시지 무시.

---

## D. 테스트 (완료 보고에 결과 포함)

단방향:
- [ ] 정상 state/iss/PKCE 연결 성공
- [ ] 거절(`access_denied`), 만료 code, 중복 callback, 다른 세션 callback, issuer 불일치 → 모두 거부
- [ ] token/secret/verifier가 HTML·browser storage·응답·로그에 노출되지 않음
- [ ] refresh 직렬화·원자 교체, 소비된 refresh 재사용 시 재연결 전환, 401 후 재연결
- [ ] 일반 기능 성공, 타인 비공개 데이터 접근 실패, 위치 조건 유지, 관리 API 403 `access_denied`
- [ ] 같은 Musecat identity를 다른 츄니인포 계정에 연결 시도 → 충돌, 기존 연결 유지

공동 연결 (C3 이후):
- [ ] 양쪽 진입점, 양쪽 로그아웃 상태, 계정 전환 중 재검증
- [ ] 잘못된 client/issuer/subject/manifest, 만료/중복 receipt
- [ ] 한쪽 교환 실패 → partial 표시, 완료로 보고 안 함
- [ ] 연결 완료 후 한쪽 해제 → 반대쪽 상태 반영
- [ ] 새 연결 뒤 지연 도착한 old-generation 해제 메시지 무시
- [ ] 연결만으로 비공개 전적이 공개되지 않음

---

## E. Musecat이 전달할 것 (츄니인포는 대기)

| 항목 | 상태 |
| --- | --- |
| 개발 서버 issuer URL | 미전달 — 배포 후 별도 전달 |
| `client_id` / `client_secret` (개발, 운영) | B1 callback 수신 후 등록·회신 |
| 역방향 Musecat callback URL (개발, 운영) | 개발 서버 URL 확정 시 함께 전달 |
| 공동 연결 Musecat 측 endpoint/인증 | A 합의 후 설계·전달 |
| 양측 개발 환경 end-to-end 검증 일정 | 위 항목 전달 후 |

---

## F. Musecat 측 현황 (2026-10-18)

구현 완료 (feature 브랜치, 배포 전):
- Musecat OAuth authorization server, `account:access` scope, `/developer/v1/account/*` 83개 operation allowlist, 관리 기능 제외.
- 역방향 external provider 연결 API (`/user/connections/*`), PKCE, 서비스별 private grant 저장, 로컬 해제.
- 공동 연결 내부 코디네이터(`JointManager`) + 원장 테이블. **공개 HTTP 엔드포인트 없음.**
- Musecat 웹: OAuth 동의 화면(로그인 레이아웃, 권한 불릿 요약, 단일 승인 버튼), 연결된 앱 관리, 외부 계정 연결 관리 화면.
- Full(운영 실행파일): guarded forward 마이그레이션 3종, 실행파일 설정(`MUSECAT_JOINT_PARTNERS`)으로 코디네이터 옵트인 준비.

10/15 전달본 이후 변경:
- `joint-connections.md`: "Implemented now" 절에 실행파일 옵트인 설정(`MUSECAT_JOINT_PARTNERS`) 설명 추가. 프로토콜·ship gate 내용은 변경 없음.
- 동의 화면 문구: 권한을 단일 문장 대신 불릿 리스트로 표시, 제외 항목에 "관리 기능" 명시. 파트너 구현에 영향 없음.
- `developer-api.md` / `developer-openapi.yaml`: 변경 없음.

아직 안 된 것(파트너 합의 대기):
- intent/receipt 어댑터, 공동 동의 UI 연결, 양측 code 발급 orchestration, durable remote-revocation outbox, 역방향 전적 수집·표시.
