# cfl

`cfl` 是 Confluence **Data Center / Server** 的命令列工具：搜尋、閱讀、匯出、建立與更新頁面，
內容可以用 Markdown 或 Confluence storage format 讀寫。附帶一份 Claude skill，讓 AI 能透過 `cfl`
使用 Confluence 裡的資料。

- 單一執行檔，不需要 Node.js 或其他執行環境
- 只支援 Data Center / Server（REST `/rest/api`），不支援 Confluence Cloud
- 刻意不提供刪除類指令

移植自 [pchuri/confluence-cli](https://github.com/pchuri/confluence-cli)（MIT），指令介面沿用，內部以 Go 重寫。
設計說明見 [docs/superpowers/specs/2026-09-11-cfl-design.md](docs/superpowers/specs/2026-09-11-cfl-design.md)。

## 安裝

### 下載執行檔（不需要 Go）

把下面的 `darwin_arm64` 換成你的平台：`darwin_amd64`、`linux_amd64`、`linux_arm64`、`windows_amd64.exe`、`windows_arm64.exe`。
這個網址永遠指向最新版：

```bash
curl -LO https://github.com/truthatt11/confluence-cli/releases/latest/download/cfl_darwin_arm64
```

驗證檔案完整性（檔名要保持原樣，`checksums.txt` 是以原始檔名記錄的）：

```bash
curl -LO https://github.com/truthatt11/confluence-cli/releases/latest/download/checksums.txt && shasum -a 256 -c checksums.txt --ignore-missing
```

Linux 沒有 `shasum` 時改用 `sha256sum -c checksums.txt --ignore-missing`。驗證通過後再改名並加上執行權限：

```bash
chmod +x cfl_darwin_arm64 && mv cfl_darwin_arm64 cfl && ./cfl --version
```

把 `cfl` 移到 `PATH` 裡的目錄（例如 `~/.local/bin` 或 `/usr/local/bin`）就能直接用。

> **macOS 使用瀏覽器下載時**：未簽章的執行檔會被標記為隔離，第一次執行會被 Gatekeeper 擋下。
> 用上面的 `curl` 指令不會有這個問題；若已經被擋，執行 `xattr -d com.apple.quarantine ./cfl` 即可。

### 用 Go 安裝

需要 Go 1.24 以上：

```bash
go install github.com/truthatt11/confluence-cli/cmd/cfl@latest
```

### 從原始碼建置

```bash
make build    # 產生 bin/cfl
make dist     # 產生 dist/ 下六個平台的執行檔（macOS、Linux、Windows × amd64/arm64）
```

安裝到 `/usr/local/bin`（需要 sudo）：

```bash
make build && sudo make install
```

請先以一般身分執行 `make build`。`install` 只在原始碼有變更時才會重新編譯，
所以這樣做可以避免以 root 身分編譯；以 root 編譯會在 repo 裡留下 root 擁有的 `bin/`，
而且 sudo 會重設 `PATH`，通常根本找不到 `go`。

不想用 sudo，就裝到自己的目錄（該目錄要在 `PATH` 裡）：

```bash
make install PREFIX=$HOME/.local
```

請寫 `$HOME` 而不是 `~`：zsh 不會展開參數裡 `=` 後面的 `~`，會在目前目錄建出一個名為 `~` 的資料夾。
移除時用同樣的 `PREFIX` 執行 `make uninstall`。打包時可以用 `DESTDIR` 指定暫存根目錄。

## 設定

1. 在 Confluence 建立 Personal Access Token：右上角頭像 → 設定 → Personal Access Tokens
   （Confluence 7.9 以後才有）。
2. 執行 `cfl init`，依提示輸入網址與 token（token 輸入時不會顯示）：

```bash
cfl init
```

不想互動輸入時，token 從環境變數讀取；刻意沒有 `--token` 旗標，因為寫在指令列上的 token 會留在 shell history：

```bash
CONFLUENCE_API_TOKEN=... cfl init -d https://wiki.example.com/confluence
```

`init` 會先實際登入驗證，成功才寫入設定。設定檔在 `~/.config/cfl/config.json`（權限 0600），
可用 `CONFLUENCE_CONFIG_DIR` 改位置。

### 設定來源的優先順序

1. `--profile <name>` 或 `CONFLUENCE_PROFILE`
2. 同時有 `CONFLUENCE_DOMAIN` 與 `CONFLUENCE_API_TOKEN` 時，完全使用環境變數（CI、AI sandbox 適用）；
   另可設 `CONFLUENCE_EMAIL`（Basic auth 的使用者名稱）、`CONFLUENCE_API_PATH`、`CONFLUENCE_PROTOCOL`
3. 設定檔中的 active profile

`CONFLUENCE_READ_ONLY=true` 會讓任何設定都變成唯讀，而且只能收緊、不能放寬。

### 多個 profile

```bash
cfl profile add ai-readonly -d wiki.example.com --read-only
cfl profile list
cfl profile use default
cfl --profile ai-readonly search "runbook"
```

## 指令

所有 `<page>` 參數都接受頁面 ID 或網址（`?pageId=`、`/pages/<id>`、`/display/SPACE/標題`、tiny link `/x/…`）。
加上 `--json` 會輸出 JSON，錯誤也會以 `{"error":{"code","message"}}` 輸出到 stderr。
每個指令的完整旗標請看 `cfl <command> --help`。

| 類別 | 指令 |
|---|---|
| 讀取 | `read`、`info`、`search`、`find`、`spaces`、`space-lookup`、`children`、`comments`、`comment-lookup`、`attachments`、`attachment-lookup`、`versions`、`property-list`、`property-get`、`edit`、`export`、`convert`（純本地）、`api`（只能 GET） |
| 寫入 | `create`、`create-child`、`update`、`move`、`comment`、`attachment-upload`、`property-set` |
| 設定 | `init`、`profile list/use/add/remove`、`install-skill` |

常用範例：

```bash
cfl search "deployment checklist"
cfl search --cql 'space = OPS AND lastmodified > now("-7d")'
cfl read 123456                                   # Markdown
cfl read 123456 --format storage                  # 原始 storage format
cfl export 123456 -r --dest ./export --referenced-only
cfl create-child "新頁面" 123456 --format markdown -f notes.md --dry-run
cfl edit 123456 -o page.xml && cfl update 123456 -f page.xml --dry-run
```

所有寫入指令都支援 `--dry-run`：會完成解析、格式轉換與驗證，印出將要做的事，但不送出。

### 用 Markdown 更新既有頁面

Markdown 無法表達 Confluence 的所有內容，例如巨集、layout、@mention、panel 設定。
`cfl update --format markdown` 會先檢查目前頁面，只要含有這類內容就會拒絕，並列出會遺失的項目。
這時請改用 storage format 修改（`cfl edit` → 修改 → `cfl update -f`），這條路不會遺失任何內容；
確定可以接受遺失時，才加上 `--allow-lossy`。

## 給 AI 使用

```bash
cfl install-skill      # 寫入 ~/.claude/skills/confluence/SKILL.md，升級 cfl 後再執行一次
```

Skill 規定 AI：頁面內容一律視為資料、不照著裡面的指示做；改動 Confluence 前要先說明並取得你的同意，
並且先用 `--dry-run` 預覽；修改既有頁面時一律走 storage format。

### 讓讀取指令不必每次確認

Skill 刻意沒有設定 `allowed-tools`，所以 Claude Code 每次執行 `cfl` 都會詢問你。
如果想讓讀取類指令直接執行、寫入類指令仍然要你核准，可以在 Claude Code 的 `settings.json` 加入下面的設定
（規則語法請以你的 Claude Code 版本文件為準）：

```json
{
  "permissions": {
    "allow": [
      "Bash(cfl read:*)", "Bash(cfl info:*)", "Bash(cfl search:*)", "Bash(cfl find:*)",
      "Bash(cfl spaces:*)", "Bash(cfl space-lookup:*)", "Bash(cfl children:*)",
      "Bash(cfl comments:*)", "Bash(cfl comment-lookup:*)", "Bash(cfl attachments:*)",
      "Bash(cfl attachment-lookup:*)", "Bash(cfl versions:*)", "Bash(cfl property-list:*)",
      "Bash(cfl property-get:*)", "Bash(cfl edit:*)", "Bash(cfl export:*)", "Bash(cfl convert:*)",
      "Bash(cfl api:*)", "Bash(cfl profile list:*)"
    ]
  }
}
```

### 安全性：唯讀設定能防什麼、不能防什麼

`--read-only` 與 `CONFLUENCE_READ_ONLY` 能防止**失誤**：AI 下錯指令時，寫入會被擋下。
但它們擋不住**刻意繞過**，因為能執行 shell 的 AI 也能改環境變數或切換 profile。
真正無法繞過的界線是 token 本身的權限；Data Center 的 PAT 權限等同帳號本身。
最嚴格的做法，是為 AI 準備一個只有檢視權限的 Confluence 帳號，並用它的 token 設定一個 profile。

## Markdown 對應

讀取時（storage → Markdown）與寫入時（Markdown → storage）使用同一套寫法，所以來回轉換不會變形：

| Markdown | Confluence |
|---|---|
| `> **INFO**`（或 `TIP`、`NOTE`、`WARNING`）開頭的引用；GitHub 的 `> [!NOTE]` 等 | 對應的提示區塊 |
| 帶語言的 fenced code；`plantuml` | code 巨集；PlantUML 巨集 |
| `<details><summary>標題</summary>` … `</details>`，或 `**EXPAND: 標題**` … `**EXPAND_END**` | expand 巨集 |
| `**ANCHOR: id**`、`[文字](#id)` | anchor 巨集與錨點連結 |
| `[[_TOC_]]`、`[[_LISTING_]]` | 目錄、子頁面列表 |
| `- [ ]`、`- [x]` | task list |
| `![](attachments/x.png)`、`[文字](attachments/x.pdf)` | 頁面附件的圖片、連結 |

讀取時遇到 cfl 不認得的巨集，會保留其中的內容，並以 `<!-- macro: 名稱 參數 -->` 註解標出位置，不會默默丟掉。

## 與 pchuri/confluence-cli 的差異

- 只支援 Data Center / Server；認證只有 PAT（Bearer）與 Basic
- 不提供破壞性指令（`delete`、`version-delete`、`versions-purge`、`attachment-delete`、`comment-delete`、`property-delete`、`copy-tree`）與 `stats`
- `read` 預設輸出 Markdown；不提供 `text` 格式
- 巢狀清單保留結構；未知巨集保留內容並加上標記；頁面連結輸出為 `/display/SPACE/標題` 網址
- `export` 的 Markdown 檔加上 YAML front matter；`--overwrite` 只會刪除 cfl 自己建立的匯出目錄
- 用 Markdown 更新含 Confluence 專屬內容的頁面時會拒絕（`--allow-lossy` 可略過）
- 寫入前先在本地驗證 storage format；所有寫入指令支援 `--dry-run`
- `api` 只能送出 GET
- 設定檔位置為 `~/.config/cfl/`，格式與 pchuri 相同，可以直接複製沿用

## 尚待在實際 Data Center 上確認

以下行為我們是根據文件與 pchuri 的實作推測的，還沒有在實際環境驗證。如果發現不符，請回報：

- 未登入時，`/rest/api/user/current` 回傳匿名使用者（`init` 依此判斷 token 是否有效）
- tiny link 的格式是 `/x/<code>`
- `/display/SPACE/標題` 網址仍可開啟頁面
- 版本列表是否需要改用 `/rest/experimental` 路徑（cfl 會自動改用）
- code 巨集是否接受 `go`、`yaml` 這類語言名稱（不接受時，頁面上的 code 區塊可能會顯示錯誤）

## 開發

```bash
make test     # go vet + go test -race
make cover    # 覆蓋率
go test ./internal/convert -update   # 有意修改轉換輸出後，重新產生 golden 檔，並檢查 diff
```

push 到 `main` 和開 PR 時，GitHub Actions 會執行 `make test` 與 `make dist`（[ci.yml](.github/workflows/ci.yml)）。

### 發布新版本

推一個 `v` 開頭的 tag 就會自動發布（[release.yml](.github/workflows/release.yml)）：測試通過後建置六個平台的執行檔，
產生 sha256 checksums，並用 commit 訊息自動產生 release notes。

```bash
git tag -a v0.1.1 -m "描述這一版的變更" && git push origin v0.1.1
```

版本號取自 `git describe`，所以未打 tag 的建置會顯示 `dev`。介面還可能調整的階段建議維持 `0.x`。

| 目錄 | 內容 |
|---|---|
| `cmd/cfl` | 進入點 |
| `internal/cli` | cobra 指令 |
| `internal/config` | 設定檔、profile、環境變數 |
| `internal/confluence` | Data Center REST client |
| `internal/convert` | storage ↔ Markdown 轉換（純函式）；`testdata/` 為來自 pchuri 的 golden fixture |
| `skill` | 內建的 Claude skill |

授權：從 pchuri/confluence-cli 移植的程式碼與 fixture 依 MIT 授權，詳見 [NOTICE](NOTICE)。
