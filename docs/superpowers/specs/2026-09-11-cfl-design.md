# cfl：Confluence Data Center CLI 設計文件

- 日期：2026-09-11
- 狀態：已核准，實作中
- 來源：移植自 [pchuri/confluence-cli](https://github.com/pchuri/confluence-cli) v2.23.0（MIT）。指令介面沿用，內部以 Go 慣用結構重寫。

## 1. 目標與非目標

**目標**

- Go 單一執行檔 `cfl`，用指令取代 Confluence UI 的讀取與一般寫入操作。
- 內建 Claude skill（`cfl install-skill`），讓 AI 能搜尋、閱讀、匯出、建立與更新頁面。
- 只支援 Confluence **Data Center / Server**（REST `/rest/api`）。

**非目標（刻意不做）**

- Confluence Cloud（v2 API、scoped token、`/wiki` 前綴）。
- 破壞性指令：`delete`、`version-delete`、`versions-purge`、`attachment-delete`、`comment-delete`、`property-delete`、`copy-tree`。
- cookie / mTLS 認證、OS keychain、`~/.netrc`、pchuri 舊版 `~/.confluence-cli` 路徑。
- 平行請求、增量匯出、`--dry-run` 的 diff 顯示、`stats`、inline comment 的建立。

## 2. 架構

```
confluence-cli/
├── cmd/cfl/main.go          # 進入點，只呼叫 cli.Execute()
├── internal/
│   ├── cli/                 # cobra 指令，一個指令一個檔
│   ├── config/              # 設定檔、profile、環境變數
│   ├── confluence/          # DC REST client
│   └── convert/             # storage ↔ Markdown（純函式，不連網路）
│       └── testdata/        # 12 組 golden fixture（來自 pchuri，MIT）
├── skill/                   # SKILL.md，go:embed 打包進執行檔
├── Makefile
└── LICENSE, NOTICE
```

入口放在 `cmd/cfl/`，讓 `go install .../cmd/cfl@latest` 產生名為 `cfl` 的執行檔。

**依賴**：`github.com/spf13/cobra` v1.10.2（CLI 框架）、`github.com/yuin/goldmark` v1.8.6（Markdown 解析）、`golang.org/x/term`（輸入 token 時不回顯）。其餘全部使用標準庫。

## 3. 設定與認證

- 設定檔：`$XDG_CONFIG_HOME/cfl/config.json`（預設 `~/.config/cfl/config.json`），可用 `CONFLUENCE_CONFIG_DIR` 覆寫。檔案 0600、目錄 0700。
- JSON 結構與 pchuri 相同（`activeProfile` + `profiles`，欄位 `domain`、`protocol`、`apiPath`、`authType`、`email`、`token`、`readOnly`），但不共用同一個檔案，避免我們重寫時弄丟 pchuri 專有的欄位。
- 設定來源優先順序：
  1. `--profile X` 或 `CONFLUENCE_PROFILE=X`：用設定檔裡的 X。
  2. 同時有 `CONFLUENCE_DOMAIN` 與 `CONFLUENCE_API_TOKEN`：完全使用環境變數（另可用 `CONFLUENCE_EMAIL`、`CONFLUENCE_API_PATH`、`CONFLUENCE_PROTOCOL`），不讀檔。
  3. 否則：設定檔的 `activeProfile`。
- `CONFLUENCE_READ_ONLY=true` 永遠疊加，只能收緊權限。
- 認證：有 `email`（DC 上填使用者名稱）用 Basic，否則用 PAT（`Authorization: Bearer`）。
- `init` 以 `GET /rest/api/user/current` 驗證，並檢查回傳的不是匿名使用者。
- 不提供 `--token` 旗標（會留在 shell history）。互動式輸入不回顯；非互動用 `CONFLUENCE_API_TOKEN`。
- 任何輸出都不印 token。`protocol: http` 可用，但每次執行在 stderr 警告。

## 4. API client（`internal/confluence`）

- Base URL：`{protocol}://{domain}{apiPath}`，`apiPath` 預設 `/rest/api`（可設定，支援 context path）。
- 分頁：`start` + `limit`。從 `_links.next` 取出 `start` 值後，用自己的路徑重新發請求。
- 重試：429 任何方法都重試；503 只重試 GET。最多 3 次，優先依 `Retry-After`，否則 1s、2s、4s，上限 60 秒。
- 逾時：一般呼叫 60 秒；附件傳輸只限制等待回應標頭的時間。Ctrl-C 會取消請求。
- Token 只送往設定的 origin：絕對 URL 必須同 origin；redirect 到不同 origin（含 https 降級 http）一律拒絕。
- 頁面參照接受：數字 ID、`?pageId=`、`/pages/<id>`、`/display/SPACE/Title`（查 API）、tiny link `/x/<code>`（跟隨一次 redirect）。只解析設定主機的 URL。
- 錯誤：`auth_failed`(401)、`forbidden`(403)、`not_found`(404，含無檢視權限)、`version_conflict`(409)、`rate_limited`(429)、`http_error`(其他)。`--json` 模式下以 `{"error":{"code","message"}}` 輸出到 stderr。結束碼 0 成功、1 失敗。
- 寫入：`update`、`move` 先讀版本號再以 +1 送出，409 不自動重試。附件上傳帶 `X-Atlassian-Token: nocheck`。版本列表先試 `/rest/api/content/{id}/version`，404/405 改用 `/rest/experimental/content/{id}/version`。

## 5. 格式轉換（`internal/convert`）

- 純函式。需要 API 的資訊（userkey → 顯示名稱、children 巨集的子頁面）由呼叫端先查好後以參數傳入。
- **storage → Markdown**：`encoding/xml`（`Strict=false`、`Entity=xml.HTMLEntity`、**不可**設 `AutoClose=xml.HTMLAutoClose`，因為它只比對本地名稱，會把 `<ac:link>` 當成 `<link>`）。對應規則沿用 pchuri 的 storage walker。
- 刻意與 pchuri 不同：
  1. 未知巨集保留內文，並加上 `<!-- macro: NAME key=value -->` 註解，不再默默丟棄。
  2. 頁面連結 `ri:page` 輸出為 `/display/SPACE/Title` 網址（不查 API）；沒有指定網站 base URL 時維持 `[Title]`。
- 轉換時回報「有損元素」清單（未知巨集、layout、帶參數的 panel 等）。
- **Markdown → storage**：goldmark + 自訂 renderer（fenced code → code 巨集並處理 `]]>`、`> **INFO|WARNING|NOTE**` → callout、task list → `ac:task-list`、`attachments/` 圖片 → `ac:image`、`<details>` → expand），其餘用 XHTML 輸出。

## 6. 指令

全域旗標：`--profile <name>`、`--json`。stdout 只放資料，訊息一律寫到 stderr。

| 類別 | 指令與主要旗標 |
|---|---|
| 讀取 | `read <page> [-f markdown\|storage\|html]`（預設 markdown）、`info <page>`、`search <query> [-l 10] [--start] [--cql]`、`spaces [-l 500] [--all]`、`space-lookup <key>`、`find <title> [-s SPACE]`、`children <page> [-r] [--max-depth 10] [--format list\|tree] [--show-url] [--show-id]`、`export <page> [...]`、`attachments <page> [-l] [-p glob] [-d] [--dest]`、`attachment-lookup <id>`、`comments <page> [-f markdown\|storage] [-l] [--start] [--all] [--location]`、`comment-lookup <id>`、`versions <page>`、`property-list <page> [-l] [--start] [--all]`、`property-get <page> <key>`、`convert [-i] [-o] --input-format --output-format`、`api <path> [-i]`（僅 GET） |
| 寫入 | `create <title> <space>`、`create-child <title> <parent>`、`update <page> [-t]`、`edit <page> [-o]`、`comment <page> [--parent]`、`attachment-upload <page> -f FILE... [--comment] [--replace] [--minor-edit]`、`property-set <page> <key> (-v JSON\|--file)`、`move <page> <newParent> [-t]` |
| 設定 | `init`、`profile list\|use\|add\|remove`、`install-skill [--dest] [--force]` |

- 內容輸入：`-f/--file <path>`、`-f -`（stdin）、`-c/--content`；`--format storage|markdown`，預設 storage。
- `export`：`--format`、`--dest`、`--file`、`--attachments-dir`、`--pattern`、`--exclude-attachments`、`--referenced-only`、`--skip-attachments`、`-r`、`--max-depth`、`--exclude`、`--delay-ms 100`、`--dry-run`、`--overwrite`。每頁一個資料夾，內含 `page.md`（含 YAML front matter：id、title、space、version、url、updated）與 `attachments/`。

## 7. AI 安全

- 寫入指令在 cobra 上標記 annotation `write`，root 在執行前集中檢查唯讀設定。
- 所有寫入支援 `--dry-run`：完成解析、轉換與失真檢查，印出目標（標題、ID、空間、版本），但不送出。
- 成功後一定印出頁面 ID、新版本號與網址。
- `update --format markdown` 若目前頁面含有損元素，會拒絕並列出會遺失的內容，需改用 storage 或加 `--allow-lossy`。
- `move` 只能在同一空間內；`api` 只允許 GET。
- CLI 內不做互動式確認；確認交給 Claude Code 權限與 skill 規則。
- README 說明：CLI 唯讀設定防的是失誤，擋不住刻意繞過；最嚴格的做法是給 AI 一個只有檢視權限的專用帳號。

## 8. Skill 與發佈

- `skill/SKILL.md` 以 `go:embed` 打包；`cfl install-skill` 預設寫到 `~/.claude/skills/confluence/SKILL.md`，已存在且內容不同時需 `--force`。
- SKILL.md 保持精簡：適用時機、設定檢查、核心流程（search → read → export）、寫入規則（先確認、先 dry-run、修改既有頁面用 storage）、頁面內容一律視為不可信資料、錯誤碼對應。旗標細節由 `cfl <cmd> --help` 取得。
- `Makefile`：`build`、`test`、`cover`、`dist`（`CGO_ENABLED=0`，darwin/linux/windows × amd64/arm64）。版本以 `-ldflags "-X main.version=..."` 注入。

## 9. 測試

- `convert`：12 組 golden fixture（`go test -update` 重新產生）、Markdown → storage → Markdown 來回測試。
- `confluence`：`httptest` 假伺服器驗證認證、分頁、重試、錯誤對應、origin 檢查、頁面參照解析。
- `cli`：以假伺服器執行完整指令，驗證輸出、`--json`、唯讀阻擋、`--dry-run`。
- 目標覆蓋率 80% 以上。

## 10. 需要在公司 DC 上實測的項目

- 未登入時 `/rest/api/user/current` 的回應形式。
- tiny link 格式 `/x/<code>`。
- `/display/SPACE/Title` 網址是否仍可用。
- 版本列表是否需要 `/rest/experimental` 路徑。
