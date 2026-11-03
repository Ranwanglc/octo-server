# Historical write-gate autopost snapshot

This branch preserves the previously uncommitted chat-to-write-gate autopost
implementation as of 2026-11-03, on its original `728e7ab` base.

It contains the webhook hook, `writegate_autopost.go`, and its synthetic tests.
The feature is off unless `WRITEGATE_AUTOPOST_ENABLED` is explicitly enabled.

Validation:

```sh
go test ./modules/webhook -run 'WriteGate|Autopost' -count=1
```

This is a separate historical integration snapshot. Its payload and
authentication have not been updated for the later service-authenticated
gateway `/submit` contract. It must not be treated as a validated companion to
the current control-plane branch without that integration work.
