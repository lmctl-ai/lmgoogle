# lmgoogle — Google Analytics, YouTube & Gmail Scripting Host for `lmscript`

**`lmgoogle`** is a single-binary Go CLI and [`lmcode-go`](https://github.com/lmctl-ai/lmcode-go) scripting host for **Google Analytics 4** (Admin API v1beta & Data API v1beta), **YouTube** (Data API v3 & Analytics API v2), and **Gmail** (API v1).

Instead of exposing hundreds of ad-hoc CLI flags, `lmgoogle` exposes a strongly typed DSL schema (`schema/lmgoogle.dsl.json`) that you script from `lmscript` (`_lmcode_` blocks). It handles authentication natively in pure open-source Go—removing any `gcloud` CLI dependency and supporting YouTube + Analytics with a single OAuth 2.0 token—and includes an in-process fake Google API server for zero-cost offline testing.

---

## Why `lmgoogle` + `lmcode`?

- **No flag explosion**: Write typed `lmscript` workflows using `load_schema(cli="lmgoogle")` and `_lmcode_` blocks instead of memorizing dozens of nested CLI flags.
- **100% Open-Source Go Auth (Zero `gcloud` Dependency)**: Built-in `lmgoogle auth login` executes the OAuth 2.0 Installed App Loopback + PKCE (`S256`) flow using `golang.org/x/oauth2`, persisting an auto-refreshing token bundle (`0600`) that powers **both** Google Analytics and YouTube from one `http.Client`.
- **Fake-by-Default Safety**: Unless `LMGOOGLE_LIVE=1` is set, `lmgoogle` runs an in-process loopback Google REST server (`internal/fakegoogle`) exercised through the real `google.golang.org/api` Go SDK clients.

---

## Quickstart

### Build & Run Offline Tests

```bash
# Build static binary
CGO_ENABLED=0 go build -o bin/lmgoogle ./cmd/lmgoogle

# Run full test suite (CLI contract, Go unit tests, and all 7 .lms examples)
./test.sh
```

### Inspect CLI & DSL Schema

```bash
./bin/lmgoogle --version
./bin/lmgoogle --help
./bin/lmgoogle --lmcode-schema
./bin/lmgoogle --lmcode-schema --json
./bin/lmgoogle --lmcode-signatures
./bin/lmgoogle --notices
```

### Authenticate for Live Google Analytics & YouTube

1. Create an **OAuth client ID (Desktop app)** in Google Cloud Console (`APIs & Services > Credentials`) with **Google Analytics Admin API**, **Google Analytics Data API**, **YouTube Data API v3**, and **YouTube Analytics API** enabled, and download `client_secret.json`.
2. Run the native OAuth 2.0 loopback flow once:

```bash
# Authorizes both Google Analytics and YouTube in one consent flow
./bin/lmgoogle auth login --client-secret ~/Downloads/client_secret.json

# Or read-only scopes only:
./bin/lmgoogle auth login --client-secret ~/Downloads/client_secret.json --readonly

# Check saved token status (secrets masked)
./bin/lmgoogle auth status
```

3. Run any `.lms` script against live Google APIs:

```bash
export PATH="$PWD/bin:$PATH"
export LMGOOGLE_LIVE=1
lmscript examples/02_ga4_traffic_report.lms
lmscript examples/05_youtube_analytics_report.lms
```

> **Note on ADC fallback**: If `LMGOOGLE_AUTH_MODE=adc` (or no OAuth2 token bundle exists), `lmgoogle` falls back to `cloud.google.com/go/auth/credentials.DetectDefault`. Standard `gcloud` ADC works for Google Analytics, while YouTube APIs require the `lmgoogle auth login` OAuth2 flow.

---

## Scripting Examples (`examples/`)

| Script | What it does |
|---|---|
| [`examples/01_ga4_property_inventory.lms`](examples/01_ga4_property_inventory.lms) | Lists GA4 accounts, properties, data streams, custom dimensions, key events; writes CSV inventory |
| [`examples/02_ga4_traffic_report.lms`](examples/02_ga4_traffic_report.lms) | Runs GA4 `RunReport` (multi-dimension + filtered), aggregates totals, exports CSV & JSON |
| [`examples/03_ga4_realtime_and_config.lms`](examples/03_ga4_realtime_and_config.lms) | Runs GA4 realtime report, inspects metadata, creates/updates/cleans up ephemeral GA4 property & web stream |
| [`examples/04_youtube_channel_and_videos.lms`](examples/04_youtube_channel_and_videos.lms) | Fetches authenticated YouTube channel, searches videos, reads stats & comment threads |
| [`examples/05_youtube_analytics_report.lms`](examples/05_youtube_analytics_report.lms) | Queries YouTube Analytics v2 channel & per-video watch time, views, likes, subscribers gained |
| [`examples/06_youtube_playlist_and_upload.lms`](examples/06_youtube_playlist_and_upload.lms) | Uploads a video (`Videos.Insert` with media), updates metadata, creates playlist, adds item, posts comment, cleans up |
| [`examples/07_cross_service_growth_brief.lms`](examples/07_cross_service_growth_brief.lms) | Correlates GA4 `Organic Video` traffic with YouTube Analytics metrics into a rendered Markdown brief |
| [`examples/08_gmail_search_and_get.lms`](examples/08_gmail_search_and_get.lms) | Searches Gmail messages (`is:unread`, `from:...`), decodes MIME bodies & headers, reads threads, exports CSV/JSON |
| [`examples/09_gmail_subject_download.lms`](examples/09_gmail_subject_download.lms) | Exports all emails matching subject/multi-query filters (default `ORDER-1005`), writes timestamped `.txt`/`.json` + attachments, and builds `index.md` |
| [`examples/10_ga4_export_channels.lms`](examples/10_ga4_export_channels.lms) | Exports all GA4 properties, web stream IDs, Measurement IDs, 30-day metrics, and `gtag.js` HTML/JS snippets into `index.md` and `channels.json` |

---

## DSL Surface (`context google: Google`)

### Safety, Auth & Ledger
- `is_live() -> bool`, `auth_mode() -> string`, `endpoint() -> string`, `run_id() -> string`
- `readonly_mode() -> bool`, `set_readonly_mode(bool) -> Google`
- `last_error() -> string`, `redact(string) -> string`
- `quota_used() -> int`, `quota_limit() -> int`, `set_quota_limit(int) -> bool`
- `ledger_count() -> int`, `ledger_entries() -> StrList`, `ledger_owns(string, string) -> bool`, `cleanup_ledger() -> int`
- `inventory_summary() -> StrMap`

### Google Analytics 4 (`analyticsadmin/v1beta` & `analyticsdata/v1beta`)
- **Accounts & Properties**: `ga_list_account_summaries`, `ga_list_accounts`, `ga_get_account`, `ga_list_properties`, `ga_get_property`, `ga_create_property`, `ga_update_property`, `ga_delete_property`
- **Streams, Custom Dimensions & Key Events**: `ga_list_data_streams`, `ga_get_data_stream`, `ga_create_web_stream`, `ga_delete_data_stream`, `ga_list_custom_dimensions`, `ga_create_custom_dimension`, `ga_list_key_events`, `ga_create_key_event`
- **Reports & Metadata**: `ga_run_report`, `ga_run_report_filtered`, `ga_run_realtime_report`, `ga_get_metadata`
- **Snapshot Types**: `GaAccount`, `GaProperty`, `GaDataStream`, `GaCustomDimension`, `GaReport` (`row_count`, `dimension_headers`, `metric_headers`, `dimension_value`, `metric_value`, `metric_float`, `metric_int`, `metric_sum`, `row_map`, `to_csv`, `to_json`)

### YouTube (`youtube/v3` & `youtubeanalytics/v2`)
- **Channels & Search**: `yt_my_channel`, `yt_get_channel`, `yt_list_channels`, `yt_search_videos`, `yt_search_videos_in_channel`
- **Videos**: `yt_get_video`, `yt_upload_video`, `yt_update_video`, `yt_delete_video`
- **Playlists & Comments**: `yt_list_playlists`, `yt_get_playlist`, `yt_create_playlist`, `yt_delete_playlist`, `yt_list_playlist_items`, `yt_add_playlist_item`, `yt_list_comments`, `yt_post_comment`
- **Analytics v2**: `yt_analytics_report`, `yt_analytics_video_report`
- **Snapshot Types**: `YtChannel`, `YtVideo`, `YtPlaylist`, `YtAnalyticsReport` (`row_count`, `column_headers`, `cell_str`, `cell_float`, `cell_int`, `col_sum`, `row_map`, `to_csv`, `to_json`)

### Gmail (`gmail/v1`)
- **Profile, Labels & Search**: `gmail_profile`, `gmail_list_labels`, `gmail_search_messages`, `gmail_get_message`, `gmail_get_thread`, `gmail_download_attachments`, `gmail_download_attachments_prefixed`
- **Send, Label & Lifecycle**: `gmail_send_message`, `gmail_modify_labels`, `gmail_trash_message`, `gmail_delete_message`
- **Snapshot Types**: `GmailProfile` (`email_address`, `messages_total`, `threads_total`, `history_id`), `GmailMessage` (`id`, `thread_id`, `subject`, `from`, `to`, `date`, `date_iso`, `timestamp_slug`, `snippet`, `body_text`, `label_ids`, `has_label`, `header`, `headers`, `attachment_count`, `attachment_names`, `attachment_ids`, `attachment_mimes`, `to_json`)

---

## Safety & Guardrails

1. **Fake-by-Default**: No live Google API calls unless `LMGOOGLE_LIVE=1`.
2. **Pre-existing Delete Protection**: `ga_delete_property`, `ga_delete_data_stream`, `yt_delete_video`, and `yt_delete_playlist` refuse to delete resources not created in the current `RunLedger` unless `LMGOOGLE_ALLOW_DELETE=1` is explicitly set.
3. **Read-Only Mode**: Set `LMGOOGLE_READ_ONLY=1` or call `google.set_readonly_mode(true)` to block all mutating operations.
4. **Automatic Secret Redaction**: `ya29.*`, `1//*`, `GOCSPX-*`, `AIza*`, and private keys are scrubbed from all host strings and error messages.
5. **Quota & Memory Guardrails**: Tracks per-run API quota units (`quota_limit`, default `10000`) and enforces `LMGOOGLE_MAX_MEMORY_MB` (default `4096`).

---

## License

MIT License — see [LICENSE](LICENSE) and [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES).
