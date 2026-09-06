# Arcade Game Delta API

이 문서는 `PUT /arcade/game`를 구현하거나 호출하는 프론트엔드 담당자를 위한
handoff 문서다. 이 API는 이제 전체 게임 목록을 `games[]`로 다시 보내는 방식이
아니라, 현재 상태를 기준으로 `add`/`modify`/`remove` delta를 보낸다.

중요: 프론트엔드는 구형 `games` payload를 보내면 안 된다. 새 요청에는
`add`, `modify`, `remove` 세 배열을 항상 포함해야 한다.

## 1. 변경 배경

기존 방식은 사용자가 게임 하나만 수정해도 현재 게임 전체와 수정된 게임 전체를
`games[]`에 넣어 서버가 전체 목록을 교체했다. 이 방식은 다음 문제가 있었다.

- 게임 하나를 수정할 때 다른 게임을 실수로 삭제할 수 있었다.
- 동시에 여러 화면/사용자가 수정하면 마지막 전체 목록이 앞선 변경을 덮었다.
- 프론트가 오래된 전체 목록을 들고 있으면 의도하지 않은 삭제가 발생했다.
- 삭제된 설치의 durable `arcade_game_id`를 재사용하는 의도를 payload만으로 표현하기 어려웠다.

새 API는 `base_state_id`로 읽은 상태를 고정하고, 실제 의도한 항목만 전송한다.
서버는 transaction 안에서 현재 immutable batch를 materialize한 뒤 delta를 적용하고,
기존의 immutable batch/changelog/rollback 흐름으로 새 상태를 만든다.

## 2. Endpoint와 request schema

`PUT /arcade/game` (bearer 인증 필요)

```json
{
  "arcade": "arcade_id",
  "base_state_id": "current_game_state_batch_id",
  "add": [],
  "modify": [],
  "remove": []
}
```

규칙:

- 세 배열은 모두 필수다. 누락하거나 `null`로 보내지 않는다.
- 세 배열이 모두 비어 있으면 `400`이다.
- 최초 등록처럼 현재 `arcade.game_v2`가 없는 경우에만 `base_state_id`를 `""`로 보낸다.
- 이후 요청은 읽어 둔 현재 `game.id` 또는 `game.state_id`를 `base_state_id`로 보낸다.
- `add`와 `modify`의 game object는 아래 필드를 포함하는 전체 객체다.
- `modify`는 partial patch가 아니다. 생략한 필드는 기존 값을 유지한다는 의미가 아니라,
  해당 필드의 유효한 새 값을 보내지 않은 것이다.
- `remove`는 `arcade_game_id` 문자열 배열이다.

Game object:

```json
{
  "id": "arcade_game_id",
  "game": "game_series_version_id",
  "cabinet": "game_cabinet_id",
  "location": "2F",
  "quantity": 2,
  "price": {
    "currency": "KRW",
    "type": "custom",
    "list": [{"value": 500}],
    "accept": []
  },
  "tag": [{"category": "기타", "note": "확인 완료"}]
}
```

검증된 cabinet이 없으면 `cabinet`을 생략하거나 빈 문자열로 보낸다. 이 경우
unverified cabinet이며, 삭제 후 재등록할 때 identity 재사용 대상이 아니다.

## 3. Response schema

성공 response는 결과 상태 전체를 반환한다.

```json
{
  "arcade": "arcade_id",
  "game": {
    "id": "new_game_state_batch_id",
    "state_id": "new_game_state_batch_id",
    "items": []
  },
  "count": 2,
  "xp_feedback": {
    "previous_exp": 10,
    "new_exp": 15,
    "diff_exp": 5,
    "previous_percent_to_next_level": 20,
    "new_percent_to_next_level": 45,
    "previous_level": 1,
    "new_level": 1,
    "level_diff": 0,
    "remaining_exp_to_next_level": 12,
    "remaining_percent_to_next_level": 55
  }
}
```

`count`는 delta 항목 수가 아니라 결과 상태의 active entry 수다. `game.id`와
`game.state_id`는 다음 요청의 `base_state_id`로 사용할 수 있다. 성공 후에는
반드시 응답의 새 state를 client state에 반영한다.

## 4. add 예시

### 최초 게임 등록

```json
{
  "arcade": "arcade_123",
  "base_state_id": "",
  "add": [
    {
      "game": "maimai_version_2026",
      "cabinet": "cabinet_dx",
      "location": "1F",
      "quantity": 2,
      "price": {"currency": "KRW", "type": "credit", "list": [{"value": 500}], "accept": ["Cash"]},
      "tag": []
    }
  ],
  "modify": [],
  "remove": []
}
```

`add`에는 `id`를 넣지 않는다. 서버가 기존 durable identity를 재사용할 수
있으면 그 id를 사용하고, 아니면 새 `arcade_game_id`를 생성한다.

### 한 게임 추가

```json
{
  "arcade": "arcade_123",
  "base_state_id": "batch_current",
  "add": [
    {
      "game": "chunithm_version_5",
      "cabinet": "cabinet_gold",
      "location": "B1",
      "quantity": 1,
      "price": {"currency": "KRW", "type": "custom", "list": [{"value": 600}], "accept": []},
      "tag": [{"category": "모니터", "note": "120Hz"}]
    }
  ],
  "modify": [],
  "remove": []
}
```

## 5. modify 예시

### 단일 게임 수정

```json
{
  "arcade": "arcade_123",
  "base_state_id": "batch_current",
  "add": [],
  "modify": [
    {
      "id": "arcade_game_abc",
      "game": "maimai_version_2026",
      "cabinet": "cabinet_dx",
      "location": "2F",
      "quantity": 3,
      "price": {"currency": "KRW", "type": "credit", "list": [{"value": 500}], "accept": ["Cash"]},
      "tag": []
    }
  ],
  "remove": []
}
```

이 요청은 `arcade_game_abc`만 교체한다. 현재 active인 다른 게임은 자동으로
삭제되지 않는다.

### 여러 게임 수정

```json
{
  "arcade": "arcade_123",
  "base_state_id": "batch_current",
  "add": [],
  "modify": [
    {
      "id": "arcade_game_abc",
      "game": "maimai_version_2026",
      "cabinet": "cabinet_dx",
      "location": "2F",
      "quantity": 2,
      "price": {"currency": "KRW", "type": "credit", "list": [{"value": 500}], "accept": []},
      "tag": []
    },
    {
      "id": "arcade_game_xyz",
      "game": "chunithm_version_5",
      "cabinet": "cabinet_gold",
      "location": "B1",
      "quantity": 1,
      "price": {"currency": "KRW", "type": "custom", "list": [{"value": 600}], "accept": []},
      "tag": []
    }
  ],
  "remove": []
}
```

같은 entry에서 필드가 여러 개 바뀌어도 XP 변경량은 1 entry로 계산한다.

## 6. remove 예시

```json
{
  "arcade": "arcade_123",
  "base_state_id": "batch_current",
  "add": [],
  "modify": [],
  "remove": ["arcade_game_abc", "arcade_game_xyz"]
}
```

삭제는 현재 state에서만 제거한다. `arcade_game_id` record나 과거
`arcade_game_history`는 지우지 않는다. 따라서 flag의 durable reference는
유지된다.

## 7. 혼합 delta

한 요청에 세 종류를 섞을 수 있다.

```json
{
  "arcade": "arcade_123",
  "base_state_id": "batch_current",
  "add": [{
    "game": "version_new",
    "cabinet": "cabinet_dx",
    "location": "3F",
    "quantity": 1,
    "price": {"currency": "KRW", "type": "custom", "list": [{"value": 700}], "accept": []},
    "tag": []
  }],
  "modify": [{
    "id": "arcade_game_abc",
    "game": "version_old",
    "cabinet": "cabinet_dx",
    "location": "2F",
    "quantity": 2,
    "price": {"currency": "KRW", "type": "custom", "list": [{"value": 500}], "accept": []},
    "tag": []
  }],
  "remove": ["arcade_game_xyz"]
}
```

서버는 현재 전체 상태를 메모리에서 복제하고 remove, modify, add를 적용한
다음 최종 상태의 중복/호환성을 검증한다.

## 8. `base_state_id`와 409 conflict

모든 성공 response의 `game.id`가 다음 요청의 base state다. 두 화면이 같은
`batch_A`를 읽고 있을 때 첫 번째 요청이 `batch_B`를 만들면, 두 번째 요청의
`base_state_id=batch_A`는 `409 Conflict`가 된다.

권장 처리:

1. 현재 화면의 `game.state_id`를 저장한다.
2. delta 요청을 보낼 때 그 값을 `base_state_id`에 넣는다.
3. `409`이면 게임 상태를 다시 조회한다.
4. 사용자가 아직 의도한 변경을 확인할 수 있으면 새 state를 기준으로 delta를 재생성한다.
5. 서버가 반환한 새 `game`을 성공 결과로 간주하고 기존 state를 유지하지 않는다.

`base_state_id` mismatch만 409이며, payload/활성 id/cabinet/version 문제는
400이다.

## 9. validation

다음은 `400`이다.

- `add`, `modify`, `remove` 중 하나라도 누락 또는 `null`
- 세 배열이 모두 빈 요청
- `add`에 `id` 포함
- `modify` id 누락, 중복, 현재 active state에 없음
- `remove` id가 비어 있거나 중복, 현재 active state에 없음
- 같은 id를 `modify`와 `remove`에 동시에 지정
- modify 대상 entry와 다른 series의 version 제출
- 최종 상태에서 같은 `game` version + `cabinet` 중복
- 존재하지 않는 version/cabinet
- `game_series_version_cabinet`에 없는 version/cabinet 조합
- quantity가 1 미만
- 기존 price, price type/list/value/accept, tag validation 위반

`modify`와 `add`의 객체는 전체 객체이므로 partial patch를 보내지 않는다.
기존 값을 유지하려면 현재 상태에서 그 값을 복사해 보낸다.

## 10. cabinet/version 규칙

- `game`은 `game_series_version` id다.
- version의 canonical `series`는 서버가 조회한다.
- entry의 series와 다른 series로 modify할 수 없다.
- 같은 series 안의 다른 version으로 modify할 수 있다.
- 비어 있지 않은 cabinet은 `game_cabinet`에 존재해야 한다.
- 비어 있지 않은 cabinet은 해당 version과 `game_series_version_cabinet` 관계가 있어야 한다.
- 최종 상태에는 동일 version + cabinet 조합을 두 번 둘 수 없다.
- cabinet을 확인하지 못한 경우 빈 문자열로 표현한다.

## 11. 삭제 후 `arcade_game_id` 재사용

삭제 후 재추가 시 id를 무조건 새로 만들지 않는다. `add`의 version에서
canonical series를 확인한 뒤 다음 순서로 처리한다.

1. 요청 cabinet이 비어 있으면 재사용하지 않고 새 entry를 만든다.
2. 현재 active batch와 충돌하는 entry를 제외한다.
3. 같은 arcade + 같은 canonical series + 같은 canonical cabinet을 가진 과거
   inactive entry를 찾는다. version 자체는 달라도 된다.
4. 후보가 여러 개이면 아래 우선순위로 하나를 선택한다.
   - matching history revision의 `created` 내림차순
   - entry record의 `created` 내림차순
   - entry id 내림차순
5. 후보가 없으면 새 `arcade_game_id`를 만든다.
6. 선택한 entry에 현재 version/location/quantity/price/tag의 새 revision을 만든다.

entry 자체는 삭제하지 않는다. 따라서 같은 identity로 다시 active가 되면
그 id를 참조하던 미해결 flag가 다시 게임 item의 `flags`에 나타난다.

### 재사용하지 않는 경우

- cabinet이 빈 문자열/누락인 unverified cabinet
- 다른 arcade의 entry
- 다른 canonical series의 entry
- 현재 batch에서 이미 active인 entry
- 다른 cabinet의 과거 entry

서로 다른 cabinet으로 재등록하면 새 identity가 생성된다.

## 12. flag와 orphan flag

flag는 `arcade_flag.game_id`에 durable `arcade_game_id`를 저장한다.

- 게임이 삭제된 동안 flag record는 자동 해결/삭제되지 않는다.
- 현재 expanded game에는 active entry의 flag만 `items[].flags`로 나온다.
- 삭제된 entry의 미해결 flag는 `game.orphanFlags`로 나온다.
- 같은 series + verified cabinet으로 재등록되어 id가 재사용되면 해당 flag는
  다시 active item의 `flags`로 이동한다.
- 새 id가 만들어지면 이전 flag는 orphan으로 남는다.

## 13. changelog

성공한 요청마다 transaction 안에서 `changed="game"` changelog 한 행을 쓴다.
`log.type`은 `game_diff`, `log.version`은 `2`다.

각 item은 다음 구조다.

```json
{
  "entry_id": "arcade_game_abc",
  "change_type": "updated",
  "before": {
    "version": "version_1",
    "cabinet": "cabinet_dx",
    "location": "1F",
    "quantity": 1,
    "price": {},
    "tag": []
  },
  "after": {
    "version": "version_2",
    "cabinet": "cabinet_dx",
    "location": "2F",
    "quantity": 2,
    "price": {},
    "tag": []
  }
}
```

`change_type`:

- `added`: 이전 state에 없고 새 state에 있음
- `updated`: 같은 entry의 값이 실제로 달라짐
- `unchanged`: modify/상태 materialize 결과 값이 동일함
- `deleted`: 이전 state에 있고 새 state에서 제거됨

`before`/`after`는 history snapshot이므로 후속 변경에도 과거 로그가 변하지
않는다. 로그의 `state_from`/`state_to`는 immutable batch id다.

## 14. XP 정책

게임 영역은 rolling 7일 window 안의 누적 고유 `arcade_game_id` 수 `n`을
계산한다. 한 요청 안에서 실제로 바뀐 entry만 후보가 되고, window 안에서
이미 계산된 entry는 다시 세지 않는다.

| rolling 7일 window의 누적 고유 entry 수 | 목표 XP |
|---:|---:|
| 0 | 0 |
| 1 | 2 |
| 2 | 4 |
| 3 | 6 |
| 4 | 8 |
| 5 이상 | 10 |

공식은 `min(10, 2*n)`이다.

새로 계산되는 entry에 포함되는 것:

- 실제 add: 1
- 값이 하나라도 다른 modify: 1
- remove: 1

포함되지 않는 것:

- 값이 모두 같은 modify: 0
- 한 entry에서 여러 필드가 변경된 경우의 추가 가산

게임 영역은 마지막 grant 기준 hard cooldown을 사용하지 않는다. 같은 user +
arcade + `game` part에 대해 rolling 7일 window를 만들고, 그 안에서 실제로
변경된 고유 `arcade_game_id`를 deduplicate한다. 누적 고유 entry 수를 `n`이라
하면 목표 XP는 `min(10, 2*n)`이고, 이번 요청에는 이미 지급된 XP를 뺀
차액만 지급한다.

예를 들어 첫 요청에서 A/B 두 entry를 수정하면 4 XP를 받고, 다음 요청에서
새 entry C를 수정하면 누적 목표가 6 XP가 되어 추가로 2 XP를 받는다. 다음
요청에서 A를 다시 수정하면 이미 계산된 entry이므로 0 XP다. 7일이 지나 A가
window에서 빠지면 다시 새 변경으로 계산된다. 다른 user, arcade, part는
별도 window다.

`xp_feedback.diff_exp`와 `user_level_log.diff_exp`에는 실제 지급된 2~10 XP가
기록된다. 실제 변경이 없거나 window 안에서 이미 계산된 entry만 수정하면 0이다.

다른 영역은 기존 정책을 유지한다.

- basic/hour/sns/gtk/photo: eligible할 때 2 XP
- admin bulk version: XP 없음
- campaign reward: 기존 별도 정책
- public-conversion reward와 historical backfill: 기존 별도 정책
- 과거 user level log와 historical backfill 기록: 소급 변경하지 않음

private arcade의 일반 game edit는 XP를 지급하지 않는다. public arcade의
일반 game edit만 위 정책을 적용한다.

## 15. transaction과 rollback

다음 작업은 하나의 transaction에 포함된다.

- current state 조회 및 delta materialize
- validation 후 entry 생성/재사용
- 새 history batch/revision 생성
- `arcade.game_v2` pointer 변경
- immutable `arcade_changelog` 기록
- public arcade의 XP ledger와 user level 갱신
- 필요한 aggregate/cache invalidation

어느 단계든 실패하면 게임 revision, pointer, changelog, XP가 함께 rollback된다.
외부 network notification은 transaction 밖의 best-effort 작업이며 성공한
mutation을 되돌리지 않는다.

## 16. 프론트 구현 checklist

- [ ] 게임 조회 response의 `game.id` 또는 `game.state_id`를 client state에 저장한다.
- [ ] 모든 요청에 `base_state_id`, `add`, `modify`, `remove`를 포함한다.
- [ ] 구형 `games[]` payload를 생성하는 코드를 제거한다.
- [ ] add object에는 id를 넣지 않는다.
- [ ] modify object에는 active id와 전체 필드를 넣는다.
- [ ] 삭제는 remove에 id만 넣고, 삭제한 object를 modify에 함께 넣지 않는다.
- [ ] cabinet이 unverified이면 빈 값으로 보내고 identity 재사용을 기대하지 않는다.
- [ ] 응답 성공 후 결과 `game` 전체를 state에 반영한다.
- [ ] 409이면 최신 상태를 다시 읽고 사용자의 의도한 delta를 재계산한다.
- [ ] 400 validation error의 `details`를 개발 로그/폼 오류에 표시할 수 있게 한다.
- [ ] `xp_feedback.diff_exp`를 서버가 지급한 실제 값으로 표시한다.
- [ ] `game.items[].flags`와 `game.orphanFlags`를 별도로 렌더링한다.
- [ ] changelog UI는 `before`/`after`와 `change_type`을 사용한다.

## 17. 권장 client state flow

```text
GET arcade detail
  -> save game.id/state_id as baseStateId
  -> user edits one or more local items
  -> build {add, modify, remove} only for intended changes
  -> PUT /arcade/game
       -> 200: replace local game state and baseStateId with response.game
       -> 409: reload current state, reconcile, rebuild delta
       -> 400: keep local draft and show validation detail
```

수정 화면은 기존 item을 그대로 복사해 전체 modify object를 만들고,
사용자가 바꾼 필드만 그 복사본에서 변경하는 방식이 안전하다. 여러 화면의
수정 draft를 합칠 때는 id 기준으로 합치되, stale base를 숨기지 않는다.

## 18. 전환 전후 payload 비교

구형 payload는 전체 목록을 보냈다.

```json
{
  "arcade": "arcade_123",
  "base_state_id": "batch_current",
  "games": ["all current game objects, including unchanged entries"]
}
```

새 payload는 의도한 변경만 보낸다.

```json
{
  "arcade": "arcade_123",
  "base_state_id": "batch_current",
  "add": [],
  "modify": ["one complete replacement object"],
  "remove": []
}
```

새 API는 `games` 필드를 읽지 않는다. 전환 기간에 구형/신형 payload를
혼용하지 말고, endpoint 전환과 client state response 처리 전환을 같은
release에서 한다.

## 19. acceptance criteria와 테스트 시나리오

기능 완료 조건:

- 단일 modify가 다른 active game을 삭제하지 않는다.
- add/modify/remove 혼합 delta가 한 immutable batch로 저장된다.
- stale base state는 409다.
- cross-series, 중복 version+cabinet, incompatible cabinet은 400이다.
- 삭제 후 같은 series + verified cabinet으로 다른 version을 add하면 기존 id를 쓴다.
- 후보가 여러 개면 history created, entry created, entry id 순으로 최신 후보를 쓴다.
- 다른 cabinet과 unverified cabinet은 id를 재사용하지 않는다.
- 재사용 identity의 미해결 flag가 active item에 다시 연결된다.
- changelog에 added/updated/unchanged/deleted와 before/after가 기록된다.
- rolling 7일 window의 누적 고유 entry 수가 0/1/2/3/4/5+일 때 목표 XP가
  0/2/4/6/8/10으로 계산된다.
- 같은 user + arcade + game part에서 이미 계산된 entry를 다시 수정하면 0 XP다.
- 새 entry는 7일 누적 목표와 이미 지급된 XP의 차액만큼 추가 지급된다.
- entry가 7일 window에서 빠지면 다시 계산되며, 다른 arcade/part/user의 window는 서로 격리된다.
- private arcade game edit는 XP를 지급하지 않는다.
- transaction 실패 시 game state, pointer, changelog, XP가 모두 원복된다.
- bulk/campaign/rollback/changelog 기존 동작이 회귀하지 않는다.

권장 backend 검증:

```bash
go test ./tests/arcade -run 'TestUpdateArcadeGame|TestGameState|TestGameCabinet|TestRollback'
go test ./...
```

## 20. 배포 순서와 주의점

1. Backend Core와 OpenAPI/문서를 먼저 배포한다.
2. 프론트가 현재 `game.id/state_id`를 저장하고 Delta payload를 생성하도록 전환한다.
3. staging에서 최초 등록, 단일 modify, 혼합 delta, 삭제 후 재등록, 409 retry,
   flag 재연결, XP feedback을 확인한다.
4. 프론트 rollout 중에는 구형 `games[]` 요청이 남아 있지 않은지 API access log와
   validation error를 확인한다.
5. Core는 fresh-bootstrap schema source이며 production data migration을 수행하지
   않는다. 기존 production 데이터에 대한 migration이 필요하면 Backend Full의
   독립적인 guarded forward migration으로 별도 작업한다.

재시도 시 같은 add를 무조건 다시 보내지 말고, 먼저 409 여부와 현재 state를
확인한다. 성공 response를 받았는데 네트워크 응답만 유실된 경우에도 stale
state를 기준으로 중복 add하지 않도록 최신 arcade detail을 다시 조회한다.
