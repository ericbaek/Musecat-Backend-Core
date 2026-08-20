# Community Translation Prototype

The prototype publishes a Korean original immediately and creates cached
English and Japanese variants after a fixed five-minute edit window.

## Configuration

```dotenv
MUSECAT_TRANSLATION_PROVIDER=deepseek
DEEPSEEK_API_KEY=your-deepseek-api-key
MUSECAT_TRANSLATION_MODEL=deepseek-v4-flash
MUSECAT_TRANSLATION_BASE_URL=https://api.deepseek.com
MUSECAT_TRANSLATION_GLOSSARY=maimai DX: preserve official spelling
MUSECAT_COMMUNITY_TRANSLATIONS_PUBLIC=false
```

When the API key is empty, the cron worker stays idle and Korean posts remain
available with `translation_status=pending`. Provider failures retry at one,
five, fifteen, and sixty-minute intervals, up to five total attempts.
DeepSeek runs in non-thinking JSON mode so translation does not pay for
unnecessary reasoning tokens. Gemini remains available by setting
`MUSECAT_TRANSLATION_PROVIDER=gemini` and its matching key, model, and base URL.

## Smoke test

Create an active-user token, then publish a Korean post:

```sh
curl -X POST http://localhost:8090/community/post \
  -H 'Authorization: Bearer YOUR_TOKEN' \
  -H 'Content-Type: application/json' \
  -d '{"title":"기체 상태","body":"CHUNITHM SUN 기체가 2층에 있습니다.","flair":"tip"}'
```

The response is immediately public through:

```sh
curl 'http://localhost:8090/community/posts?locale=ko-KR'
```

After five minutes, confirm that `translation_status` becomes `ready`. While
`MUSECAT_COMMUNITY_TRANSLATIONS_PUBLIC=false`, an English or Japanese request
still returns Korean with `translated=false`. After reviewing stored results,
enable the feature and restart the service:

```dotenv
MUSECAT_COMMUNITY_TRANSLATIONS_PUBLIC=true
```

```sh
curl 'http://localhost:8090/community/posts?locale=ja-JP'
```

The response then uses the cached Japanese variant with `translated=true`.
No provider call occurs during a read request.

## Phase-one boundaries

- Originals are labeled `ko-KR`; phase one assumes Korean authors and does not
  run a separate language-detection call.
- Posts support an optional game series and flair; missing flair becomes
  `chitchat`.
- Comments, reactions, mentions, reports, and media are intentionally deferred.
- Core defines the fresh-bootstrap schema. Backend Full must add the equivalent
  schema through its own guarded forward migration before deployment.
