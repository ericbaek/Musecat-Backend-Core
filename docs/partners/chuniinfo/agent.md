# CHUNITHM Info ↔ Musecat 연동 작업 지시서

작성 기준: 2026-10-15. 대상: 츄니인포 개발자 및 해당 저장소에서 작업하는 코딩 에이전트.
이 파일을 작업 프롬프트로 전달하고, 함께 제공되는 `developer-api.md`와
`developer-openapi.yaml`을 참고한다. Musecat의 `feat/external-account-connections`
구현을 기준으로 작성한 계약이며, 배포 완료나 양측 API의 최종 합의를 뜻하지 않는다.

## 1. 작업 목표와 경계

츄니인포 사용자가 ‘뮤즈캣 연결’을 누르면 뮤즈캣으로 이동해 로그인하고, 앱 정보와
권한 요약을 확인한 뒤 한 번 승인한다. 이후 츄니인포 화면에서 연결한 뮤즈캣 사용자의
일반 기능으로 오락실 조회·정보 수정·사진 등록·고장 신고·반응 등을 수행한다.
항목마다 별도 체크박스를 요구하지 않는다. 관리 기능은 포함하지 않는다.

별도로 뮤즈캣 프로필에 츄니인포 전적을 표시하는 역방향 연동도 준비한다.
두 방향의 OAuth client/provider와 token은 분리하되, 공동 동의 계약에 따라 **하나의 최종 승인**으로
두 권한을 발급하는 흐름을 목표로 한다. 표준 OAuth 승인 한쪽만 받고 반대 권한을 추정해서 만들지 않는다.
계정을 자동 병합하거나 로그인 수단을 대체하지 않는다. 상세 설계는 동봉 `joint-connections.md`를 읽는다.

**목표 UX:** 츄니인포 ‘뮤즈캣 연결’ → 필요한 양쪽 로그인 → 두 계정과 양방향 권한 요약 →
‘연결하고 계속하기’ 한 번 → 양측 확인 후 완료. 양쪽 계정이 이미 로그인돼 있으면 로그인 단계는
생략될 수 있지만, 다른 계정의 소유 확인이나 최초 동의를 생략하는 의미는 아니다.

먼저 이 저장소의 AGENTS.md, 인증·세션·API 구조를 읽고 기존 코드를 보존한다.
기존 기능 브랜치에서 최소 변경으로 구현하고, main에 직접 푸시하지 않는다.
프로덕션 설정·실사용자 데이터는 변경하지 않는다.

## 2. 지금 모르는 값: 설정 주입으로 준비

**개발 서버 URL은 추후 Musecat 측이 별도 전달한다. 지금은 알 수 없다.**
이 문서의 경로나 예시 도메인으로 실제 배포 주소를 추측하지 않는다.
localhost 테스트 프록시·임시 포트·테스트 앱 주소를 파트너 설정에 복사하지 않는다.

다음 이름은 츄니인포 측에 제안하는 설정 이름이며 현재 츄니인포 구현을 가정하지 않는다.

| 설정 | 전달/확정 주체 |
| --- | --- |
| `MUSECAT_OAUTH_ISSUER` | Musecat이 추후 전달하는 정확한 HTTPS issuer origin |
| `MUSECAT_CLIENT_ID` | Musecat 운영자가 등록 후 전달 |
| `MUSECAT_CLIENT_SECRET` | Musecat 운영자가 별도 비공개 채널로 전달; 서버 전용 |
| `MUSECAT_REDIRECT_URI` | 츄니인포가 실제 callback HTTPS URL 제안 → Musecat이 정확히 등록 |

개발·운영 client/secret/callback을 분리한다. 미설정이면 연결 버튼을 비활성화하거나
미설정 상태를 명시한다. 임의 기본 URL로 요청하지 않는다. 공개 프론트 환경변수에 secret을 넣지 않는다.

issuer를 받은 후 `GET {issuer}/.well-known/oauth-authorization-server`에서 authorization,
token, revocation endpoint를 읽는다. metadata의 `issuer`가 설정값과 정확히 같은지 확인한다.
호스트는 운영자가 신뢰한 고정 설정만 사용한다. 사용자가 입력한 URL을 서버에서 호출하지 않는다.
공용 참조는 `{issuer}/developer`, `{issuer}/developer/guide.md`,
`{issuer}/developer/openapi.yaml`이다. 배포 전에는 함께 전달받은 파일로 구현한다.

## 3. 현재 구현된 단방향 OAuth 계약 (공동 연결의 구성 요소)

아래 token/PKCE/resource 계약은 현재 Musecat 구현이다. 이것만 순서대로 두 번 호출하고
양방향 원클릭이 완성됐다고 표시하지 않는다. 공동 동의·intent·receipt 프로토콜은 7절의
파트너 확정이 필요하며, 두 개의 기존 승인 화면을 자동으로 승인하거나 우회하지 않는다.

Musecat은 authorization server, 츄니인포 서버는 confidential OAuth client다.
Authorization Code + PKCE S256, token/revoke 인증은 `client_secret_basic`이다.
password/implicit grant, browser-only client, OIDC ID token, 임의 UID 연결은 제공하지 않는다.

### 연결 시작 및 callback

1. 츄니인포 로그인 세션에 연결 요청을 바인딩한다. state는 추측 불가능한 난수로 만들고
   PKCE verifier(43–128자), S256 challenge와 함께 서버에서 단기 보관한다.
2. 브라우저를 metadata의 authorization endpoint로 리다이렉트한다.
   - `response_type=code`
   - `client_id=<등록값>`
   - `redirect_uri=<정확히 등록된 HTTPS callback>`
   - `scope=account:access`
   - `state=<요청별 난수>`
   - `code_challenge=<base64url(SHA256(verifier)), padding 없음>`
   - `code_challenge_method=S256`
3. Musecat 로그인·동의 화면은 Musecat이 소유한다. 츄니인포가 사용자 토큰을 요구하거나
   `/oauth/consent`를 대신 호출하지 않는다. 비밀번호나 Musecat 세션 JWT를 받지 않는다.
4. callback은 initiating 츄니인포 세션, 미사용 state 및 정확한 `iss`를 검증한다.
   `code`, `state`, `iss`, `error`의 중복 파라미터와 code/error 동시 전달을 거부한다.
   `error=access_denied`는 정상적인 거절로 처리하고 토큰을 교환하지 않는다.
5. 코드 수명은 90초이며 한 번만 교환한다. 서버에서 HTTP Basic client 인증으로
   token endpoint에 form-urlencoded POST한다:
   `grant_type=authorization_code`, `code`, `redirect_uri`, `code_verifier`.
   Basic의 client ID/secret은 OAuth 규칙에 따라 각각 form-encode한 뒤 결합·base64한다.
6. 응답은 `access_token`, `refresh_token`, `token_type=Bearer`, `expires_in`, `scope`다.
   실제 scope에 `account:access`가 있는지 확인한다. 기존 좁은 권한 토큰을 자동 승격하지 않는다.
7. `GET {issuer}/developer/v1/me`에 access token을 보내 `{sub, username, nickname}`를 확인한다.
   연결 식별자는 **(issuer, client registration, sub)** 로 저장한다. 서버의 현재 츄니인포 사용자에
   연결하며, 브라우저가 보낸 Musecat UID·email·nickname을 계정 소유 증명으로 사용하지 않는다.
   다른 츄니인포 계정에 이미 연결된 identity를 자동 이전하지 않고 충돌로 처리한다.
8. callback의 코드/query를 화면·분석·로그에 남기지 않도록 일반 설정 화면으로 이동한다.

브라우저에는 츄니인포 자체 세션 쿠키만 보관한다. access/refresh token, secret, verifier는
서버 전용이며 저장할 토큰은 암호화한다. API는 츄니인포 BFF/서버에서 호출한다.

### 사용할 API와 금지된 API

- 단일 연결 scope: `account:access`.
- 일반 기능 경로: `/developer/v1/account` + 문서에 명시된 사용자 기능 경로.
  예: `GET /developer/v1/account/arcades`, `PUT /developer/v1/account/arcade/basic`,
  `POST /developer/v1/account/arcade/photo/upload`, `POST /developer/v1/account/arcade/flag`.
- 현재 명시적으로 허용된 83개 method/path는 동봉 OpenAPI를 기준으로 한다.
  임의의 기존 API 앞에 prefix만 붙이면 된다고 가정하지 않는다. 새 기능은 계약 확인 후 추가한다.
- query/body/응답/multipart 파일 필드는 해당 operation schema를 그대로 사용한다.
  사진 업로드와 오락실 사진 적용은 별도 operation이며 업로드 성공만으로 게시 완료로 표시하지 않는다.
- 사용자 소유권·공개/비공개·현재 이용 제한·위치/GPS·캠페인 조건·XP/이력 규칙은 그대로 적용된다.
  로그인/연결은 위치 인증을 대신하지 않는다. 위치가 필요한 기능은 사용자 동의를 받아 실제 값을 전달한다.
- **관리·검토·운영 기능은 관리자 계정을 연결해도 사용할 수 없다.**
  가입·탈퇴·인증정보 변경·다른 앱 연결 승인·역방향 계정 연결·raw PocketBase API도 제외된다.
- opaque access token은 `/developer/v1/*` 전용이다. `/user/*` 같은 일반 세션 경로에 보내거나
  Musecat 로그인 JWT로 교환하지 않는다.
- `/developer/v1/me`의 앱별 `sub`와 `/developer/v1/account/user/me`의 일반 프로필 `id`는 다른 값이다.
  identity에는 `sub`, 오락실/프로필 리소스 참조에는 각 API가 반환한 리소스 ID를 사용한다.
- 기존 6개 좁은 scope와 resource alias는 호환용으로 남는다. 신규 연결은 `account:access`를 사용한다.

### 갱신·해제·실패 처리

- access token 최대 600초. `expires_in`을 기준으로 갱신한다.
- refresh token은 매번 회전하며 family의 절대 수명은 30일이다. 갱신이 수명을 연장하지 않는다.
  연결별 갱신 요청을 직렬화하고 새 access/refresh 쌍을 원자적으로 교체한다.
- 소비된 refresh/code 재사용은 해당 현재 grant를 취소한다. 네트워크 timeout으로 결과를 모르면
  같은 code/refresh를 무조건 재전송하지 않는다. 안전하게 재연결이 필요한 상태로 처리한다.
- refresh는 Basic 인증 + form `grant_type=refresh_token`, `refresh_token`으로 token endpoint에 POST한다.
- 해제는 Basic 인증 + form `token=<refresh token>`으로 revocation endpoint에 POST한다.
  성공은 200이며 알 수 없는 토큰도 200이다. access 또는 refresh 취소는 앱 grant 전체를 취소한다.
- 해제 시 로컬 credentials와 진행 중 연결 요청을 폐기한다. 원격 revoke 실패는 사용자에게 구분해
  알리고, Musecat 설정에서도 해제할 수 있게 안내한다. 로그/재시도에 평문 토큰을 남기지 않는다.
- 사용자가 Musecat에서 해제하거나 계정 상태/키가 바뀌면 다음 호출이 실패할 수 있다.
  현재 구현은 파트너에 해제 webhook을 약속하지 않는다. 401/invalid_grant면 무한 재시도 대신 재연결로 전환한다.
- 403은 기능/위치/권한 부족일 수 있으므로 재연결을 강요하지 말고 API 오류를 표시한다.
  제외된 등록 경로는 `access_denied`, 미등록 경로는 404/405일 수도 있다.
- 429는 서버 정책에 맞춰 대기한다. 변경 요청을 무조건 재전송해 중복 등록하지 않는다.
  범용 idempotency-key 지원은 현재 계약에 없으므로 임의로 있다고 가정하지 않는다.

## 4. 역방향: 츄니인포 전적 → Musecat (추가 협의 필요)

Discord에서 요약 전적의 테스트 API 연동은 확인됐으나, 아래 항목의 최종 wire contract는
이 문서 작성 시 확정되지 않았다. 기존 츄니인포 OAuth/developers 구현을 조사해 제안서를 만들고,
필드나 endpoint를 새로 추측해 운영 연결하지 않는다.

- 츄니인포/미래 rhythm.info의 issuer, authorization/token/user-info endpoint, client 인증 방식,
  Musecat callback 등록, 실제 scope 이름, token 만료·회전·취소 동작.
- user-info가 반환할 검증된 최상위 **문자열 subject**. Musecat 현재 adapter는 설정한 필드명을
  읽으며 숫자/중첩 subject는 추가 adapter 협의가 필요하다. email/닉네임은 식별자가 아니다.
- 기본 프로필 후보: 현재/최고 레이팅, OVER POWER, 칭호·레벨·팀, 플레이 보면 수,
  FC/AJ 수, 랭크 분포, 마지막 플레이/동기화 시각. 항목 전체 제공을 이미 확약한 것으로 처리하지 않는다.
- 베스트 곡·최근 플레이 이력의 내부 표시 범위/페이지네이션/외부 링크 위치는 추가 합의한다.
  아웃링크만 가능한 것이 확정된 것은 아니다. 데이터 출처는 CHUNITHM Info로 명시한다.
- 비공개 전적은 공개 Musecat 프로필에 노출하지 않는다. 비공개 전환·권한 해제·캐시 만료 시
  기존 데이터도 숨길 수 있는 응답/갱신 계약이 필요하다.
- Musecat의 역방향 연결 ledger는 현재 계정 확인·서비스별 private grant 저장까지 지원하며,
  공개 전적 제공 API·공개 필드 설정·전적 수집/표시의 완성이나 원격 revoke를 의미하지 않는다.
  양방향 해제는 각각 별도이며 자동 동기화를 가정하지 않는다.

고정 파트너 API key가 있더라도 사용자 계정 소유 확인과 비공개 정책을 대체할 수 없다.
다른 UID를 넣어 아무 계정이나 연동하는 방식을 만들지 않는다.

## 5. rhythm.info 통합과 다중 플랫폼

- 브랜드·도메인·게임 이름과 인증 identity를 분리한다. 하나의 provider identity에 CHUNITHM,
  SOUND VOLTEX 등 서비스별 grant를 연결할 수 있는 구조를 사용한다.
- chuni.info → rhythm.info issuer 변경은 같은 사용자라고 자동 간주하지 않는다.
  검증된 subject 매핑/전환 계획을 별도 합의하거나 사용자에게 다시 연결받는다.
- OAuth client가 변경되면 Musecat의 앱별 sub도 달라질 수 있다. email 매칭이나 기존 grant 복사로
  이관하지 않는다. client/권한 설정 변경은 기존 grant를 무효화하므로 재동의를 계획한다.
- 같은 subject 문자열이라도 issuer/client가 다르면 다른 identity다.
- 계정 통합 일정, subject 유지 가능 여부, callback·issuer 전환 시점은 아직 확인 항목이다.

## 6. 에이전트 구현 순서와 완료 기준

1. 기존 인증/BFF/저장소 구조 조사 → 수정 파일·연결 identity 모델·callback 정책을 짧게 정리한다.
2. 위의 미정 설정을 외부 주입하는 시작/callback/token storage/refresh/revoke를 구현한다.
   issuer 미정 상태에서는 고정된 로컬 mock으로 검증하며 운영 URL을 만들지 않는다.
3. 연결 관리 UI와 오락실 읽기·쓰기 호출을 붙이고 기존 츄니인포 기능을 보존한다.
   첫 검증은 조회 + 일반 사용자 수정 1개 + 고장 신고/사진 흐름으로 한다.
4. 다음 자동 테스트를 통과시킨다:
   - 정상 state/iss/PKCE 연결; 거절·만료·중복 callback·다른 세션·issuer 불일치 거부.
   - 토큰/secret이 HTML, browser storage, 응답, 로그에 노출되지 않음.
   - 갱신 직렬화 및 원자 교체; 재사용/해제/401 후 재연결 처리.
   - 정상 일반 기능 성공, 타인 비공개 데이터 접근 실패, 위치 조건 유지, 관리 API 사용 불가.
   - client/issuer/계정 충돌 시 기존 연결을 자동 덮어쓰지 않음.
5. Musecat이 실제 URL과 비공개 credentials를 전달하고 callback을 등록한 후, 양측 개발 환경에서
   연결 → 조회 → 수정 → 갱신 → 양측 설정 해제 → 재연결을 검증한다. 그 전에는 통합 성공으로 보고하지 않는다.
6. 역방향 API는 기존 구현 조사 결과와 확정이 필요한 표를 Musecat 담당자에게 전달한다.
   운영 배포/계정 이전/새 scope 확약은 개발자 확인 없이 진행하지 않는다.

원클릭 연결 작업은 아래 7절을 우선 적용한다. 기존 단방향 API 구성 요소를 보존하고,
공동 연결 기능은 합의된 adapter가 준비되기 전까지 비활성화한다.

완료 보고에는 변경 파일, 테스트 결과, 미정 값, API 차이/질문을 포함한다.
이 문서만으로 츄니인포에 새 권한이나 데이터 제공을 일방적으로 확약하지 않는다.

## 7. 양방향 원클릭 공동 연결 계약 초안 (이번 변경)

### 양측이 합의해야 활성화할 수 있는 사항

- 최종 동의 화면은 Musecat이 제공한다는 안을 기준으로 구현을 검토한다. 츄니인포는 이 화면이
  ‘Musecat 일반 기능 사용’과 ‘츄니인포 전적 제공’을 함께 승인받는 권한을 갖는지 명시적으로 합의한다.
  UI 문구·목적·scope·게임 서비스·공개 정책을 versioned consent manifest로 고정한다.
- 츄니인포는 로그인된 사용자에게 **승인 전 연결 intent**를 준비한다. 이는 로그인을 증명하는
  계정 맥락이며, 전적 접근 token을 발급했다는 뜻이 아니다. browser에는 opaque handle만 전달한다.
- Musecat 서버가 고정된 파트너 endpoint를 서버 인증으로 호출해 intent의 진위, issuer, client,
  츄니인포 subject, callback, scope/services, manifest, 만료·미사용 상태를 확인한다.
  정확한 경로·요청/응답·인증 방식은 양측이 추가 확정한다. 이 파일에 새 endpoint가 배포됐다고 가정하지 않는다.
- 사용자 최종 승인 후 Musecat은 자체 grant/code를 발급한다. 츄니인포는 해당 joint approval을
  서버 간 일회성 확인으로 검증한 뒤 자기 서버에서 Musecat용 grant/code를 발급한다.
  한쪽 access token을 다른 쪽 token으로 재사용하지 않는다. 각 방향의 state/PKCE/issuer 검사를 유지한다.
- intent/receipt는 최대 5분, 추측 불가능한 일회성 handle, 원자적 소비, 인증된 client와 두 계정 맥락,
  연결 요청 ID·정확한 callback·manifest에 바인딩한다. handle 원문은 DB/로그에 남기지 않는다.
  bool, UID, 미검증 서명 문서로 승인 완료를 주장하지 않는다. 서버 간 인증은 목적/audience를 별도 정의한다.
- partner가 승인 확인을 받았다고 응답하는 것과 token 교환이 실제 완료된 것은 구분한다.
  양쪽 완료 acknowledgment는 안전하게 반복할 수 있지만 code/refresh 교환을 무조건 재시도하지 않는다.
- 두 서버 사이에는 단일 DB transaction이 없다. 한쪽 실패 시 ‘일부 연결됨’으로 남기며 완료로 표시하지 않는다.
  기존에 독립적으로 승인된 연결을 중간 실패 때문에 임의 취소하지 않는다.

### Musecat에 지금 준비된 기반

- `connections.JointManager`와 locked `external_joint_connection`: pending/partial/connected/cancelled
  영속 상태, 5분 요청 수명, 세션·provider/client·서비스·verified subject 바인딩.
- 내부 `Start` → `RecordVerified` → `Status` / `Cancel`. **공용 HTTP API가 아니다.**
  미래 trusted callback/receipt adapter만 호출하며 browser가 evidence ID나 subject를 보내 완료 처리할 수 없다.
- 실제 교환된 Musecat `account:access` grant generation와 같은 사용자의 partner connection을
  검증한다. partner issuer/subject/services/private grant 및 현재 config/account restriction을 확인한다.
- 재동의·해제·연결 revision 변경 시 이전 ‘연결 완료’ 표시는 degraded/invalidated로 바뀐다.
  내부 Status는 로컬 ledger 확인이며 파트너의 실시간 token 유효성/remote revoke를 보장하지 않는다.
- `Cancel`은 조정 요청만 취소한다. 현재 개별 OAuth grant, 외부 credentials와 권한은 별도이며,
  사용자용 ‘양쪽 연결 해제’ UI로 이 함수를 바로 연결하면 안 된다.
- 아직 receipt adapter, 공동 consent UI wiring, 양측 code 발급 orchestration,
  durable remote-revocation outbox가 없다. 따라서 지금 전체 원클릭 기능은 활성화하지 않는다.

### 츄니인포 에이전트가 먼저 준비할 작업

1. 기존 OAuth/세션 구현을 조사해 intent/confirm/revoke 서버 adapter의 구체적인 제안과
   manifest/scopes/string subject 계약을 작성한다. URL·secret이 미정이면 설정 주입과 mock만 사용한다.
2. 서버에 session-bound intent와 일회성 receipt state를 준비한다. 인증된 파트너와 scope/계정/manifest를
   검증하기 전에는 code나 token을 발급하지 않는다. 기존 일반 OAuth 경로는 유지한다.
3. 두 계정 표시·공동 권한 요약·단일 최종 승인·partial/expired/retry UI를 구현할 수 있게 준비한다.
   기존 `account:access`에 관리자 기능을 추가하거나 전적을 기본 공개로 변경하지 않는다.
4. 양방향 해제는 pair ID와 정확한 generation을 기준으로 한다. 로컬 권한은 즉시 끊고 원격 실패는
   별도로 표시한다. 중복·순서가 바뀐·이전 세대의 취소 메시지가 재연결된 새 권한을 취소하면 안 된다.
   endpoint 합의 후 durable outbox/reconciliation을 구현한다. 기존 one-way unlink는 로컬 전용이다.
5. 테스트에 account switch, 잘못된 client/issuer/subject/manifest, 만료/중복 receipt,
   한쪽 교환 실패, 연결 완료 후 한쪽 해제, 새 연결 뒤 지연 도착한 old-generation 해제를 추가한다.
6. 위 계약을 담당 개발자가 확정하고 실제 개발 서버/등록값을 받은 후에만 공동 연결 adapter를 켜고
   두 서비스에서 end-to-end 검증한다. 아직 정해지지 않은 partner scopes·endpoint를 운영 구현으로 확약하지 않는다.
