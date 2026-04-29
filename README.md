# mattermost-myagents-plugin

Mattermost users can talk to their personal opencode agent through the `@myagents` bot.

## Behavior

- Public/private channel mention: `@myagents` text is sent to the user's personal opencode server and the response is posted in the original thread.
- Direct message with `@myagents`: the full message is treated as the prompt, and replies are threaded so the RHS thread can stay open for the next turn.
- Group DM: only messages mentioning `@myagents` are handled.
- Bot, webhook, plugin, and empty messages are ignored or answered with guidance.
- Attachments are not supported in the MVP.

## User Mapping

The plugin maps the Mattermost message author to a personal opencode URL:

```text
https://{userid}.{BaseDomainSuffix}
```

The default mapping uses the Mattermost username, lowercases it, converts dots/underscores to hyphens, and validates it as a DNS label.

## opencode Integration

The plugin creates an opencode session with `POST /session`, stores the returned session ID in the Mattermost Plugin KV Store, sends prompts through `POST /session/:id/prompt_async`, and streams responses from `GET /event`.

Both official opencode bus events such as `message.part.updated` and wrapper-style SSE events such as `message_start`, `message_delta`, and `message_end` are handled. Reasoning parts and Qwen-style `<think>` / `</think>` output are shown as temporary thinking panels that fade out as the final answer appears.

## JupyterHub Integration

Users can control their personal JupyterHub-backed opencode server from Mattermost:

- `켜줘` or `서버 켜줘`: start server
- `꺼줘` or `서버 꺼줘`: stop server
- `상태 알려줘`: show server status

JupyterHub API calls use `Authorization: token <token>`.

## Admin Settings

- `BotUsername`
- `BaseDomainSuffix`
- `UserIDMappingMode`
- `RequestTimeoutSec`
- `EnableAsync`
- `AsyncThresholdSec`
- `JupyterHubBaseURL`
- `JupyterHubAPIToken`
- `AutoStartServer`
- `ServerStartTimeoutSec`
- `ServerStopTimeoutSec`
- `ServerStatusPollIntervalSec`
- `AllowUserStopServer`
- `AllowUserStartServer`
