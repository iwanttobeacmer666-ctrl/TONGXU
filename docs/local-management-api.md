# Local management API

The Windows application can use the existing trusted local broker. File locations remain unchanged. No HTTP listener, new dependency or separate application account is required by this backend change.

Send one UTF-8 JSON object to the standard input of:

```powershell
bin\group-assistant.exe -manage-local -operator-settings "D:\MY_SHARE\group-assistant\runtime\operator-setup.json"
```

The broker checks the Windows user, integrity and container restrictions before connecting to the named pipe. The native worker cannot use this control channel. The owner is the single administrator.

## Requests

| Action | Fields | Result |
|---|---|---|
| `list` | none | Groups, members, enabled flags, lifecycle states, configuration `Version` |
| `status` | none | Structured service/platform/agent status, owner and configuration `Version`; no roster or secrets |
| `service-stop` | `Version` | Accept a graceful service-stop request through the existing shutdown lifecycle; never a cleanup-completion acknowledgement |
| `update-pause`, `update-resume` | `Version` | Pause/resume new task and media admission; preserve in-flight work and approval controls |
| `update-stop` | `Version` | Stop only after paused tasks, cleanup and callback delivery drain; otherwise return `Code: busy`. Shutdown follows the reply/ACK attempt |
| `group-add` | `Version`, `Group` | Add and enable a group immediately |
| `group-enable`, `group-disable` | `Version`, `Group` | Update the live group gate; disabling cancels that group's tasks and pending approvals/confirmations |
| `add`, `enable`, `disable` | `Version`, `Group`, `User` | Update a member immediately, including membership preferences in a disabled group |
| `tasks` | optional `Group` | Structured `TaskItems` for active and recent tasks; omitting `Group` includes all groups |
| `task-detail` | `Task`, optional `Group` | Task state, timestamps and bounded execution events |
| `stop` | `Task`, optional `Group` | Stop the task and wait for cleanup; publication already in progress finishes first |
| `documents` | `Group`, optional `User`, `Offset`, `Limit` | Paged file metadata; `Limit` defaults to 50 and is at most 100 |
| `document-read` | `Group`, optional `User`, `Name`, `Offset`, optional `FileVersion` | Up to 64 KiB of base64-encoded `Data`, total size, next offset and SHA-256 |
| `document-delete-preview` | `Group`, optional `User`, `Name` | Actor/group/file-version-bound confirmation in `Documents.Pending` |
| `document-delete` | `Version`, `Group`, `Confirm` | Confirm the staged document deletion |
| `documents-delete-preview` | `Version`, `Group`, optional `User`, `Names`, optional `Limit` | Preview deletion of 1–100 selected relative file names in `FileOperation` |
| `documents-delete` | Matching `Version`, `Group`, optional `User`, `Confirm` | Execute the selected-file preview |
| `documents-clear-preview` | `Version`, `Group`, optional `User`, optional `Limit` | Preview the entire selected public/private area, including subdirectories |
| `documents-clear` | Matching `Version`, `Group`, optional `User`, `Confirm` | Clear the previewed area; preserve its root, membership, dependencies and sessions |
| `temporary-files` | optional `Offset`, `Limit`; no group/user | Paged metadata for the shared task temporary area; advisory `Busy` flag |
| `temporary-clear-preview` | `Version`, optional `Limit`; no group/user | Preview globally idle temporary clearing, including unattributed crash remnants |
| `temporary-clear` | Matching `Version`, `Confirm`; no group/user | Clear the reviewed manifest while globally idle; preserve the root |
| `file-operation` | `Version`, `Confirm`, `Scope`, optional `Group`, `User`, `Offset`, `Limit` | Page a preview or its results; `Scope` is `selected`, `documents` or `temporary` |
| `delete-preview` | `Version`, `Group`, `User` | Preview deletion of a disabled member's private data and membership |
| `delete-preview` with `Scope: "all"` | `Version`, `User` | Preview the same member across all configured groups; every affected membership must be disabled first |
| `delete` | Matching scope/user/group, `Version`, `Confirm` | Execute the matching `DeletePreview.Token`; public data remains |
| `cleanup-retry` | `Version`, `Group`, optional `User` | Recheck cleanup after a failure; cannot bypass unfinished deletion |

For documents, omitted `User` means the administrator; `User: "public"` selects public files. Other values must be registered stable member IDs. File names are relative to that scope, using `/` for subdirectories. Requests do not accept host file paths or shell commands.

## Desktop status and lifecycle

`Status.UpdatePaused` reports the update admission fence, allowing a new desktop instance to resume admission after an interrupted update wait. Update actions require the same trusted local broker and current configuration version; they do not acknowledge that the process has exited or all process locks have been released. The installer helper separately waits for those locks. Installed per-user deployments resolve operator settings under LocalApplicationData/Tongxu/runtime; existing development deployments keep the example path above.

Replies include the stable administrator ID in `Owner`. The `status` action returns `Status.Service` as `offline`, `running` or `stopping`; `Status.Platform` as `stopped`, `connecting`, `connected` or `unknown`; and `Status.Agent` as `unchecked`, `ready` or `unavailable`. `Started` and `ConnectedAt` are UTC timestamps; an unset timestamp has its zero value. `ConnectedAt` is the last successful subscription acknowledgement, not proof of a current connection. Only `Platform: connected` denotes the current acknowledged connection. A connected WebSocket alone is insufficient. `Agent: ready` reports the native boundary readiness check; it does not certify login, model availability or a completed model request.

`status` uses the same trusted broker and safe offline locks as `list`. `Success: true, Online: false` with `Service: offline` is confirmed offline. Transport errors or uncertain locks are not equivalent to offline. Neither status nor accepted global stop sets `CleanupReady: true`.

`service-stop` requires the current `Version`. After the reply/ACK attempt, the server cancels the original main lifecycle, which stops the platform and waits for existing task shutdown. New mutations are refused while stopping; list and task inspection remain available while the endpoint exists. Poll until confirmed offline to observe process exit. Offline is not a blanket confirmation that all temporary remnants were removed. No force kill, automatic restart or startup cleanup is added.

The desktop settings adapter, `Desktop-Operator.ps1`, exposes `read`, `save`, `launch` and `login` over child-process stdin/stdout. It invokes `bin/group-assistant.exe -check-local-client` first, reusing the existing Windows user/integrity/container restrictions without reading settings or contacting any platform. This flag is mutually exclusive with other execution modes. The adapter reuses the original setup validation, service/config locks, DPAPI storage, atomic save, native startup checks and dedicated login scripts. It returns only a masked robot ID and a credential-presence flag. `save` requires its own snapshot `Version` from `read`; do not substitute the management roster version. Existing credentials remain when credential inputs are empty. Connection/path editing is offline and applies next startup, with no migration. The adapter accepts only existing path keys, not arbitrary settings fields. Desktop appearance preferences are separate in `runtime/desktop-settings.json` and do not affect group permissions or persistent file layout.

Use the SHA-256 from the first read as `FileVersion` on subsequent chunks. A changed file is rejected rather than silently mixing versions. A document delete confirmation expires after five minutes and is consumed once. File contents are not embedded in membership or task listings.

Administrator reads now hash files as a stream and retain only the requested 64-KiB window. Each block rechecks the full file, so memory is bounded but every read costs a full scan. Administrator listing and deletion no longer inherit the worker's 8-MiB file restriction or 1,000-file listing restriction. Worker uploads, snapshots and group chat file commands retain their existing limits. Administrator inventory has a separate safety bound of 100,000 file/directory entries; exceeding it fails without truncation or deletion.

## Batch deletion and complete-area clearing

New file operations require the authenticated local service. Obtain `Version` from `list`. Preview and execute with the same configuration version, group, user and operation kind. Tokens bind these fields, the administrator, the opened root identity and the relative names/types/content hashes. A current version cannot rebind an older token. Single-file document deletion also binds the original configuration version and target user; local management tokens cannot be confirmed through the unversioned group `/confirm` command.

`FileOperation` returns:

| Fields | Meaning |
|---|---|
| `Token`, `Kind`, `Group`, `User`, `Version`, `Expires` | Bound operation identity; `User` is the stable member/owner ID or `public`, matching request format |
| `Files`, `Directories`, `Bytes` | Original manifest totals; bytes count file contents |
| `Total`, `NextOffset`, `Items` | Paged inventory or results; the root itself is excluded |
| `ReviewComplete` | All original preview entries have been delivered in order |
| `Consumed` | Execution has started; another execution is forbidden |
| `Complete` | Execution finished with no failures and, for a clear, no remaining entries |
| `Busy` | Advisory activity flag on temporary inventory, not permission to clear |
| `Deleted`, `Failed`, `Remaining` | Removed entry count, failure count and remaining entries after a clear; counts include directories |

Each item has `Name`, `Bytes`, optional `SHA256`, `Directory`, `Blocked`, `Status` and `Code`. Names are relative to the selected server-owned area, never arbitrary host paths.

The preview returns its first page. Continue with `file-operation`, `Scope` equal to its `Kind`, the original token/version/group/user, and the returned `NextOffset`. Pages default to 50 entries and allow at most 100, with an encoded-byte budget that can shorten a page. Always use the returned offset. The backend requires delivery of the complete manifest in order before confirmation. The desktop must present that complete, browsable list and totals to the administrator.

Tokens expire after five minutes. They are consumed before filesystem validation/work; stale content, changed roots or execution failures require a fresh preview. Incomplete review or a busy temporary area does not consume the token, but expiry still applies. Results remain pageable until expiry or cache eviction; execution cannot be replayed. At most eight plans/results are held in memory. When full, consumed results are evicted oldest-first; eight outstanding previews cause `busy` until expiry. Restart discards previews/results. Result reads use the original version even after configuration changes; they cannot execute or rebind an operation.

Before removing any entry, the backend validates the complete relevant manifest. It rechecks each file before removal and returns `deleted`, `failed`, or `not_attempted` for each entry. Item codes include `changed_or_unavailable`, `cancelled`, `preview_changed_or_unavailable`, and `new_entry`. Successful removals are not rolled back when another file fails. A failed clear reports remaining entries; newly discovered entries are listed but not deleted.

Incomplete execution returns `Success: false` and `FileOperation.Complete: false`, even if some files were removed. `CleanupReady` becomes true only for completed execution. A successful preview, list or page does not mean cleanup occurred. `Applied` stays false because file operations do not edit configuration.

Selected deletion uses exactly `Names`, independently of pagination/search. One preview accepts 1–100 distinct names; larger selections must be split into explicit batches. Complete-area clearing includes the entire selected group's public area or one user's private area, including empty subdirectories. Do not send `Name`, `Names`, or filtering fields to narrow a clear request; incompatible fields are rejected. Clearing files preserves enrollment, dependencies, Codex sessions, public conversation context and other file areas. Public-file clearing is an explicit administrator operation; member deletion still preserves public files.

Requests remain one strict UTF-8 JSON object, now bounded to 64 KiB for selected names. Replies remain bounded to 256 KiB. Unknown fields and arbitrary host paths are rejected. New actions expose machine-readable `Code`: `offline`, `version_conflict`, `invalid_scope`, `inventory_limit`, `busy`, `changed`, `confirmation_invalid`, `invalid_request`, or `unavailable`. New execution/inventory work has a 30-second broker context budget. Transport failure means an uncertain result: read the staged result or refresh the file list; never automatically replay a mutation.

For example, send each object separately through `-manage-local`:

```json
{"Action":"documents-delete-preview","Version":"<list.Version>","Group":"<group-id>","User":"<member-id>","Names":["report.docx","notes/summary.txt"],"Limit":50}
{"Action":"documents-delete","Version":"<preview.Version>","Group":"<group-id>","User":"<member-id>","Confirm":"<FileOperation.Token>"}
```

Fetch all intervening `file-operation` pages when needed before confirming. To clear public files, use `documents-clear-preview` / `documents-clear` with `User: "public"` and omit `Names`.

## Shared temporary area

Temporary inventory exposes names, types and sizes, without file contents or guessed ownership. It includes `turn-*`, `control-*`, `.ccga-thread-delete-*`, empty directories and unowned crash remnants inside configured `WorkDir`. Group/user filtering is rejected. Inventory marks links or unsupported entries as `Blocked`; these prevent a clear preview and require administrator inspection. Document deletion previews also reject links and symlinked scope roots. Reserved `.ccga-` document entries block complete-area previews rather than being deleted.

Both preview and execution acquire the admission fence nonblockingly and check for downloading, queued, running, approval-waiting, publishing or cleaning-up tasks. The native runner must be healthy. Control inspection and native member/session deletion also exclude temporary clearing. Busy operations are rejected; no task is stopped automatically. During validation/deletion the fence prevents new task admission. Task records remain present until snapshot cleanup, including tasks that have left admission but have not yet created their copy.

Only configured temporary contents are eligible. The root is retained; filesystem roots, aliases and overlap with persistent files, dependencies, state or Codex home are refused. Unknown remnants become eligible through this explicit global preview/confirmation, never through startup or inferred membership. Clearing temporary files does not reset persisted member lifecycle failures. After investigating the original failure, use `cleanup-retry` or a fresh member-deletion preview as appropriate.

## Applying changes

Fetch `list`, use its `Version`, then adopt the returned version after each successful write. Stale versions are rejected. `Applied` means the settings were saved; `CleanupReady` separately reports cleanup confirmation. Do not treat `Success` alone as proof that a disable finished cleaning up.

Online acknowledgement means subsequent admissions use the new group/member roster. Disabling all groups leaves the local service available with an empty admission list. Enabling a group preserves its members' individual enabled preferences and reloads its saved public context.

Offline `list` and roster edits still work under the configuration/service locks and apply at the next startup. Task, document and member-data deletion operations require the local service. A connection failure or uncertain reply never silently falls back to offline writes. Refresh before retrying an uncertain mutation.

## Lifecycle and deletion

Member/group records may report `enabled`, `disabling`, `disabled` or `cleanup_failed`. Legacy records without `State` use their existing `Enabled` flag. Member deletion additionally uses `deleting` and `delete_failed`. Failure states survive a service restart. Repeating enable/disable cannot erase an incomplete cleanup or deletion state.

`cleanup-retry` requires a healthy native runner, finished relevant tasks and no remaining task-copy directories. It does not claim recovery if these checks fail. For an interrupted deletion, obtain another deletion preview and finish deletion instead.

A member deletion preview lasts five minutes. It records the selected group/user identities, private document/dependency content fingerprints and all recorded native thread IDs. A changed file or mismatched scope invalidates the preview. Deletion removes native threads through `thread/delete`, private files, scoped dependencies, private conversation/ownership records and recent task details, then removes enrollment. Public files, public group history, other members and shared authentication remain intact. A failure leaves enrollment disabled and allows a fresh preview/retry.

The existing random-name task copies share `WorkDir`. Member deletion waits until this temporary area is empty; active work can finish or be stopped by the administrator. Unattributed remnants after a crash block deletion instead of being guessed to belong to a member or silently left behind while reporting complete deletion. These remnants need explicit administrator inspection/cleanup.

Conversation ownership persists across `/new` and restart. Current legacy mappings are imported into the ownership ledger; unknown historical threads are never guessed to belong to a member. The one-off authorized test-data reset removed the project's previous unknown history before this feature was implemented.

Task detail exposes tool names/commands, public execution output and lifecycle timestamps. Model reasoning is omitted. Known credentials and common token/authorization patterns are redacted; raw protocol maps are not returned. Events are capped at 40 per task, text at 1,000 bytes per event, and finished-task history at 50 tasks in memory. No percentage is inferred. Task/document replies omit the unrelated group roster to preserve frame capacity. The desktop client can poll task details and use event sequence numbers to identify new entries.

## Validation scope

File-management regressions use temporary directories, synthetic runners and a real same-user local pipe. They cover reads beyond 8 MiB, scopes beyond 1,000 files, selected and recursive clearing, full-preview paging, wrong identity/scope/version, stale/expired/replayed confirmations, offline refusal, downloading/running/queued/control work, new-task exclusion, root replacement, unhealthy-runner refusal, late unpreviewed files and Windows deletion sharing violations. Real WeCom and model calls are not part of these tests.

Windows directory-junction tests cover temporary entries, nested document entries and cross-user scope-root aliases. The separate symbolic-link test skips when Windows denies link creation; tests do not request additional privileges or change security policy. A synthetic-child regression forces Go stack growth during a pending pipe connection. The broker pins asynchronous buffers until completion, including cancellation, as required by the [Windows I/O lifetime contract](https://learn.microsoft.com/en-us/windows/win32/api/ioapiset/nf-ioapiset-cancelioex).

Behavioral tests cover hot groups and idle admission, scoped/global member deletion, stale previews, public/other-user preservation, old-thread ownership, paged document reads, stale deletes, credential redaction and lifecycle recovery guards. Installed-native deletion is checked separately with a temporary Codex home: empty/already-deleted threads and a materialized thread created through a local synthetic response fixture. Deletion itself must not trigger inference. No real login, account or model is used. Real multi-user WeCom acceptance remains an operator check.
