# CHUNITHM Info 연동 체크리스트

작성 기준: 2026-10-18. 2026-10-15 전달본(`agent.md`, `developer-api.md`, `developer-openapi.yaml`,
`joint-connections.md`)의 **후속 문서**다. 그 사이 Musecat이 연동 계약을 확정했다:

- **[`partner-integration-spec.md`](../../partner-integration-spec.md)** — 모든 외부 플랫폼에 공통으로 적용되는 Musecat 연동 스펙 v1. 양방향 OAuth 프로파일, 전적 API envelope, 원클릭 공동 연결 프로토콜(intent/receipt/revoke 엔드포인트·스키마·인증·에러코드), 양방향 해제, 온보딩 양식을 Musecat이 정했다.
- **[`developer-center.md`](../../developer-center.md)** — 파트너가 셀프 등록 → 자동 검사 → Musecat 승인 1회 → 즉시 활성화되는 개발자 센터 설계.

따라서 **츄니인포가 결정할 사항은 없다.** 스펙대로 구현하고, 아래 값을 채워 제출하면 된다.
이전에 보낸 "양측이 합의해야 할 사항" 항목은 전부 스펙에서 Musecat이 확정한 것으로 대체된다.

---

## 1. 연동 범위 선택 (하나만 체크)

| | 범위 | 스펙 섹션 | 필요한 구현 |
| --- | --- | --- | --- |
| [ ] | **A만**: 츄니인포에서 뮤즈캣 일반 기능 사용 | §2 | OAuth client 구현 |
| [ ] | **A + B**: 위 + 뮤즈캣 프로필에 츄니인포 전적 표시 (각 사이트에서 따로 승인) | §2, §3 | + OAuth 서버 프로파일, 전적 API |
| [ ] | **A + B + Joint**: 위 + 한 번 승인으로 양방향 연결 | §2–§6 | + 공동 연결 endpoint 3개, 해제 알림 |

권장: A + B + Joint. 단계적으로 가도 되며 (A 먼저 승인받고 나중에 B/Joint 추가), 추가 시 재승인만 받으면 된다.

---

## 2. 제출할 값 (온보딩 양식, 스펙 §8)

Musecat 개발 서버와 운영 서버 각각 1부. 개발자 센터 공개 전까지는 이 JSON을 Musecat 담당자에게 보낸다.

### 2.1 공통

| 필드 | 값 |
| --- | --- |
| `partner_id` | `chuniinfo` (고정, 승인 후 변경 불가) |
| `display_name` {ko,en,ja} | [ ] |
| `description` {ko,en,ja} | [ ] |
| `contact.email` | [ ] |

### 2.2 Direction A (츄니인포 → 뮤즈캣)

| 필드 | 요구 사항 | 값 |
| --- | --- | --- |
| `redirect_uris[]` | 정확한 HTTPS, 1–8개, query/fragment 없음, localhost 불가 | [ ] |

→ 승인 시 Musecat이 `client_id`, `client_secret`(1회 노출) 발급. scope는 `account:access` 고정.

### 2.3 Direction B (뮤즈캣 → 츄니인포) — A+B 이상 선택 시

| 필드 | 요구 사항 (스펙 §3.1–3.3) | 값 |
| --- | --- | --- |
| `issuer` | 고정 HTTPS origin. authorization 응답에 `iss` 필수 (RFC 9207) | [ ] |
| `authorization_url` | 고정 HTTPS. PKCE S256 필수, `plain` 거부 | [ ] |
| `token_url` | `client_secret_basic`. refresh token **필수**, family ≥ 30일. 응답에 `scope` 포함 | [ ] |
| `userinfo_url` | 최상위 문자열 `sub`(1–256자, 안정, issuer 내 유일). `iss` 있으면 issuer와 일치 | [ ] |
| `revocation_url` | RFC 7009, Basic 인증, 모르는 토큰도 200 | [ ] |
| `records_url` | `GET {records_url}/{service_id}` → `musecat-records/v1` envelope (§3.4). 비공개면 403 `records_private` | [ ] |
| `client_auth_method` | `client_secret_basic` | 고정 |
| `identity_scope` | 스펙 기본값 `profile`. 기존 이름이 다르면 실제 문자열 | [ ] |
| `services[]` | `{"id": "chunithm", "scope": "<실제 scope>"}`. 스펙 §1.1 registry ID만 사용 | [ ] |
| `musecat_client_id` / `musecat_client_secret` | 츄니인포가 Musecat용으로 발급. Musecat callback은 승인 시 Musecat이 전달 → 정확히 등록 | [ ] |

전적 envelope `fields`/`sections`에 어떤 항목을 넣을지는 츄니인포가 정한다(스펙이 타입·개수·크기만 제한). 후보: 레이팅/최고 레이팅, OVER POWER, 칭호·레벨·팀, 플레이 수, FC/AJ 수, 랭크 분포, 마지막 플레이, 베스트 30(`sections`), 외부 링크(`source.url`, `more_url`).

### 2.4 Joint (원클릭) — A+B+Joint 선택 시

| 필드 | 요구 사항 (스펙 §4–6) | 값 |
| --- | --- | --- |
| `manifests` | `["musecat-joint/v1"]` | 고정 |
| `joint_url` | 아래 3개 endpoint의 base URL | [ ] |
| `connect_start_url` | 뮤즈캣에서 시작할 때 브라우저를 보낼 URL (§5.5) | [ ] |
| `musecat_to_partner_key` | 츄니인포가 발급, ≥ 32 bytes base64url, OAuth secret과 별개 | [ ] |
| `key_rotation_window_hours` | 기본 24 | [ ] |

→ 승인 시 Musecat이 `partner_to_musecat_key` 발급(1회 노출).

---

## 3. 구현 체크리스트

### 3.1 Direction A (스펙 §2, `developer-api.md`)

- [ ] 설정 주입(issuer, client_id, client_secret, redirect_uri), 미설정 시 버튼 비활성
- [ ] `/.well-known/oauth-authorization-server`에서 endpoint 로드, `issuer` 일치 검증
- [ ] 세션 바인딩 state + PKCE S256, `scope=account:access`
- [ ] callback: state·`iss` 검증, 중복 파라미터/`code`+`error` 동시 거부, `access_denied` 정상 처리
- [ ] token 교환(Basic), 90초 code 1회, scope 확인
- [ ] `GET /developer/v1/me` → `(issuer, client_id, sub)` 저장, 타 계정 연결 충돌 처리
- [ ] token 암호화 저장, 브라우저엔 자체 세션만, API는 서버에서
- [ ] refresh 직렬화·원자 교체·30일 family, revoke + 로컬 폐기, 401→재연결 / 403→기능 오류 / 429→대기
- [ ] 1차 기능: 조회 → 수정 1개 → 고장 신고 → 사진 업로드+적용

### 3.2 Direction B (스펙 §3)

- [ ] Musecat을 confidential client로 등록 (PKCE S256, Basic, callback 정확 등록)
- [ ] authorization 응답 `iss` 포함, `plain` PKCE 거부
- [ ] userinfo: 최상위 문자열 `sub` (+ 선택 `name`, `services`)
- [ ] scope: identity scope는 userinfo만, `<service>:read`는 해당 서비스 전적만
- [ ] refresh token 발급, revocation endpoint
- [ ] `GET {records_url}/chunithm` → `musecat-records/v1` envelope, 비공개 시 `records_private`, ≤ 256 KiB, `updated_at` 정확

### 3.3 Joint (스펙 §4–6)

- [ ] 서버 간 인증: Musecat 요청은 `Authorization: Bearer <musecat_to_partner_key>` 검증, Musecat 호출 시 `partner_to_musecat_key` 사용, `X-Request-Id` 10분 멱등
- [ ] intent 발급: 로그인 사용자에 바인딩, ≥ 32 bytes 난수, 해시만 저장, 5분, `code_challenge`(Direction A용 PKCE) 포함
- [ ] `POST {joint_url}/intents/lookup` — 응답 스키마 §5.3, 소비/만료 전까지 반복 조회 가능
- [ ] `POST {joint_url}/receipts` — intent **원자적** 소비, manifest 확인, Musecat용 `partner_code` 발급(≤ 90초, Musecat client·redirect_uri·code_challenge 바인딩), `generation`/`correlation_id` 저장. 200 응답 **후에만** `musecat_code` 교환, 실패 시 재시도 금지·partial 표시
- [ ] `POST {joint_url}/revocations` — `pair_id`+`generation` 정확 일치만 해제, 이전 세대 → `unknown_generation`, 중복 → `already_revoked`
- [ ] `connect_start_url`: 로그인 → intent 생성 → `return_to`로 `?intent=` 리다이렉트 (`return_to`는 등록값 검증)
- [ ] 완료 표시는 자체 교환 성공 + `GET /partner/v1/joint/{correlation_id}` = `connected`일 때만
- [ ] 양방향 해제: 로컬 즉시 차단 → Musecat `POST /partner/v1/revocations` durable 전송(≥ 24h 재시도)
- [ ] 에러 코드: `invalid_intent`, `intent_expired`, `intent_consumed`, `manifest_unsupported`, `subject_mismatch`, `unauthorized_partner`

---

## 4. 검증 및 제출 절차

1. [ ] 스펙 §9 케이스를 자체 테스트로 통과 (적합성 키트 공개 전까지는 자체 테스트 결과 첨부)
2. [ ] 온보딩 양식(§2) 작성 — 개발 서버용 먼저
3. [ ] Musecat 개발자 센터에 제출 (공개 전에는 담당자에게 JSON 전달)
4. [ ] 자동 검사 결과 확인, 실패 항목 수정 후 재제출
5. [ ] Musecat 승인 → `client_id`/`client_secret`/`partner_to_musecat_key` 1회 수령 → 설정 반영
6. [ ] 개발 환경 end-to-end: 연결 → 조회 → 수정 → 갱신 → 해제(양쪽) → 재연결
7. [ ] 운영 서버용 양식 제출 → 동일 절차

---

## 5. Musecat 측 일정 (츄니인포는 대기)

| 항목 | 상태 |
| --- | --- |
| Direction A 서버 (`account:access`, 83 ops) | 구현 완료, 배포 대기 |
| Direction B 어댑터 (OAuth client 측) | 구현 완료 |
| 전적 envelope 수집·캐시·프로필 표시 (§3.4) | 미착수 |
| Joint Musecat 측 endpoint + 동의 화면 연결 + 해제 outbox (§4–6) | 미착수 |
| 개발자 센터 (셀프 등록·자동 검사·원클릭 승인) | 설계 완료, Phase 1(Direction A) 착수 예정 |
| 적합성 키트 | 설계 완료, 미착수 |
| 개발 서버 URL | 배포 후 전달 |

Direction A는 Musecat이 배포하는 즉시 연동 가능하다. B와 Joint는 츄니인포가 먼저 구현해 두면
Musecat 측 구현이 끝나는 대로 재승인 없이(양식은 동일) 활성화된다.

---

## 6. 10/15 전달본과 달라진 점

- `agent.md` 7절 "양측이 합의해야 활성화할 수 있는 사항" → 전부 스펙에서 Musecat이 확정. 합의 대신 구현.
- `joint-connections.md`의 "Proposed … requires partner agreement" → "specified". 논리 흐름은 동일, 구체 endpoint는 스펙 §5.
- 역방향 userinfo subject 필드명은 `sub`로 고정 (설정형 `subject_field` 폐기).
- 서버 간 인증은 양방향 static bearer key로 고정.
- 전적 API는 자유 스키마가 아니라 `musecat-records/v1` envelope.
- 온보딩은 개발자 센터 제출로 통일; 승인 1회로 활성화.
