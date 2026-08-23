# gits 需求規格書（v1）

> 狀態：草案 · 初稿 2026-08-23 · 第二版 2026-08-23
> 適用版本：v1（首個可用版本）
> 本文件的目標是「照著寫就能開工」——v1 的每個指令行為、manifest 結構、錯誤處理、機器可讀輸出與退出碼都在此定案；v2 以後的構想集中在最後一章，不展開細節。

---

## 1. 背景與問題

開發者在一個工作目錄底下同時維護十幾個彼此相依的 git repo（本文件以 `example-workspace` 這個 workspace 為第一個實例：18 個 repo，橫跨兩個遊戲專案、共用的平台服務、以及唯讀的參考專案）。這種「多重 repo」工作流有五個反覆出現的痛點：

1. **換機器接續工作**：在公司與家裡兩台電腦之間切換，每次都得逐一 `cd` 進去 `git pull`。漏掉一個，就在舊程式碼上開始工作。
2. **不知道自己漏了什麼**：一次改動橫跨多個 repo 之後，很難確認是不是每個都 commit、每個都 push 了。未推送的 commit 往往要等到另一台機器發現「怎麼沒有這段」時才浮現。
3. **repo 清單本身會漂移**：某台機器上新增或退休的 repo，另一台機器完全不知情。單純掃描資料夾解不了這個問題——不存在的東西掃不出來。
   > 實例：`example-workspace` 的說明文件載明 `stack-tools` 是兩個遊戲共用的 stack 工具，但這台機器上根本沒有這個目錄；反過來上游已退休的 `game-docs` / `arcade-docs` 卻還躺在硬碟上。
4. **跨 repo 的版本相依看不見**：A repo 的某個 commit 依賴 B repo 的某個 commit。改動 B 之後，沒有任何機制提醒「A 釘的還是舊版」。
   > 實例：`example-workspace` 裡有 9 個 repo 把 `shared-proto` 掛成 submodule，釘住 **7 種不同的 SHA**，落後幅度從 2 到 18 個 commit，其中 `arcade-client-cli` 釘的那個 SHA 與 `main` **分歧**（領先 3、落後 3）而非單純落後。這張依賴表已經存在於檔案系統中，只是沒有人把它讀出來。
5. **AI agent 讀到的是一份會漂移的散文地圖**：workspace 的 `CLAUDE.md` 手寫了一張 repo 對照表（名稱／語言／角色／是否唯讀）——那正是 manifest 的內容，只是用散文寫的、靠人維護的。實測它已經同時往兩個方向漂移。
   > 實例：`CLAUDE.md` 與 `docs/claude/*.md` 記載了 `stack-tools`、`puzzle-game-server`、`legacy-synth`、`arcade-server-plan` 四個 repo，硬碟上**一個都不存在**；其中 `stack-tools` 被提及 16 次，還包含「`stack-tools/stack.sh` 假設每個服務都是直接 sibling」這類操作指示。任何 agent 進到這個 workspace，都會先讀到一份自信滿滿的假地圖，然後花好幾個 turn 撞牆。

`gits` 要解的就是這五件事。

### 1.1 兩種使用者，同等重要

`gits` 有兩類呼叫者，設計上必須等價對待：

| 呼叫者 | 特性 | 對 `gits` 的要求 |
| --- | --- | --- |
| **人** | 有終端機、會讀對齊的表格、能回答 `[y/N]`、會忽略反覆出現的警告 | 掃一眼就懂的輸出、可預期的動詞、假警報要少 |
| **AI agent** | 無 TTY、無法回答提問、只能 parse 結構化輸出、會重試、context 有限、錯誤時傾向盲目重試 | **絕不阻塞**、每個指令都有穩定的 `--json`、錯誤要有代號而非散文、輸出要有決定性 |

對人類，`gits` 是「省下 18 次 `cd` + `git pull`」的效率工具。**對 agent，`gits status --json` 是它取得「這個 workspace 現在到底有什麼」的唯一可信來源**——散文文件必然漂移，`gits` 讀的是檔案系統與 git metadata，不會。

---

## 2. 為什麼不用現成方案

開工前調查過現有生態，結論是「有很多接近的，沒有一個到位的」：

| 類型 | 代表工具 | 提供什麼 | 落差 |
| --- | --- | --- | --- |
| 批次操作型 | gita、mani、mu-repo、meta、myrepos | 平行對 N 個 repo 執行 status/pull/push 或任意指令 | 不理解 repo 之間的相依；批次 commit 的支援薄弱或危險 |
| manifest 型 | Google repo、tsrc、vcstool、git-ws | 用 manifest 描述整組 repo（URL／分支／SHA），可補齊缺少的 repo、可凍結版本 | 偏向單向 checkout 與 CI 重現場景，不處理「你改了 B，A 可能被你弄壞」這個反向問題 |
| 組織批次型 | ghorg、git-xargs、git-workspace | 把整個 GitHub/GitLab organization 拉下來、批次改動並開 PR | 針對組織維運，不是日常開發循環 |
| Git 原生 | submodule、subtree | submodule 能精確釘住「A 依賴 B@SHA」 | 完全不處理批次操作；把整組 repo 變成 superproject 的 submodule，在「多數 repo 天天在改」的情境下 UX 成本過高 |

上述工具還有一個共通落差：**幾乎都沒有穩定、有版本的機器可讀輸出**，其「輸出」就是給人看的對齊文字。要讓 agent 使用，只能去 parse 那些排版，這是最脆弱的整合方式。

**結論**：批次操作的部分是成熟輪子，重造是為了合手（跨平台、單一執行檔、與既有工作流一致）；**依賴破壞偵測沒有現成品，是 `gits` 真正的原創價值**。而且它不需要發明新格式——`.gitmodules` 加上 gitlink SHA 已經是一張現成的依賴表，`gits` 只要把它反向解讀即可。

---

## 3. 設計原則

1. **絕不自作主張——而且不只靠「問」。** 會改動工作區或推送到遠端的行為，對人類預設先報告計畫並取得確認。但確認提示的安全性建立在「回答的是人」這個前提上；agent 會自己帶 `-y`，那道確認就形同虛設。因此安全性同時建立在**結構性限制**上：`no-write` 邊界、`--max-repos` 上限、`--dry-run` 一律可用。限制對所有呼叫者一律生效。
2. **失敗不擴散。** 單一 repo 的失敗不中斷其餘 repo；結尾一定給完整摘要，不讓錯誤淹沒在捲動的輸出裡。
3. **`gits` 不取代 git。** 它是排程與彙整層，實際操作一律委派給 `git` 執行檔。任何 `gits` 做不到的事，使用者都能 `cd` 進去用 git 完成，不會被鎖在工具裡。
4. **預設離線、明確連網。** 唯讀查詢預設只讀本機 refs，速度以毫秒計；需要連網的動作以旗標明示。
5. **跨平台一級公民。** Windows 與 Linux 行為一致，不假設 POSIX shell、不假設路徑分隔符號、不假設終端機支援任意 ANSI 能力。
6. **報告誠實。** 資料過期就說過期，判斷不確定就說不確定。`deps` 只回報「落後／分歧／不一致」這些事實，不宣稱「一定壞掉」。
7. **雙受眾等價。** 人在終端機看到的每一件事實，agent 都能透過 `--json` 取得同樣的事實。不存在「只有人看得到」的資訊。
8. **永不阻塞。** 非互動環境下絕不等待輸入——無論是 `gits` 自己的提示，還是它呼叫的 `git` 子行程。寧可快速失敗並說清楚缺什麼，也不要留下一個沒有輸出、不會結束的行程。

---

## 4. 核心概念

| 概念 | 定義 |
| --- | --- |
| **workspace** | 一個包含 manifest 的目錄。其下的受管 repo 由 manifest 列舉。 |
| **manifest** | workspace 根目錄的 `gits.yaml`，宣告這組 repo 有哪些、從哪來、屬於哪些群組。應納入版本控制（見 §5.6 與 §10.1），讓 repo 清單能跟著同步到另一台機器。 |
| **本地覆寫** | workspace 根目錄的 `gits.local.yaml`，不納入版控，只描述「這台機器的例外」。見 §5.5。 |
| **repo 條目** | manifest 中的一筆記錄。`name` 同時是預設的目錄名稱。 |
| **根 repo** | 當 workspace 根目錄本身也是一個 git repo 時（存放共用文件、`CLAUDE.md`、工具腳本等），它是 manifest 中 `path: "."` 的那一筆條目，與其他 repo 同等受管。見 §5.4。 |
| **群組（group）** | repo 的標籤，可多重歸屬。用於 `-g` 篩選，例如只操作 `game` 這組。 |
| **no-write** | repo 條目的旗標。標記為 `no-write` 的 repo 參與所有唯讀指令（`status`／`sync`／`deps`／`list`），但被所有寫入類指令（`commit`／`push`／`foreach`）自動排除。用於他人擁有、你只是拉來參考或建置的 repo。這個名字描述的是**行為邊界**（別幫我寫），不是檔案權限。 |
| **本尊（canonical checkout）** | 當某個 repo 同時「以 submodule 形式被別的 repo 依賴」且「本身也是 workspace 中的一員」時，workspace 中的那份 checkout 即為本尊。`deps` 用它的 commit graph 當權威時間軸。 |

---

## 5. manifest 規格

### 5.1 檔案位置與格式

- 檔名固定為 `gits.yaml`，位於 workspace 根目錄。
- 格式為 YAML（與 tsrc／vcstool／mani 同一族，日後互轉容易）。
- 讀寫皆透過 YAML 節點層級 API 進行，`gits adopt` / `gits add` 寫回時**必須保留既有註解與格式**。
- **`gits` 是 `gits.yaml` 的唯一寫入者。** Agent 不應直接編輯這個檔案——一般的 YAML 序列化會把註解與排版洗掉，而註解正是「為何此 repo 標為 no-write」這類資訊的所在。要以程式方式新增條目，用 `gits add`（§7.9）。

### 5.2 結構

```yaml
# gits workspace manifest
version: 1

# 所有 repo 的預設值，可被個別條目覆寫
defaults:
  remote: origin
  branch: main

repos:
  # 根 repo：workspace 目錄自己也是一個 repo，存放共用文件與清單本身
  - name: workspace
    path: "."
    url: https://git.example.com/gravity/workspace.git
    groups: [workspace]
    description: workspace 自身（文件、CLAUDE.md、gits.yaml）

  - name: game-server
    url: https://git.example.com/game/game-server.git
    groups: [game]
    description: 核心輪盤遊戲伺服器

  - name: vendor-sdk
    url: https://git.example.com/gravity/vendor-sdk.git
    groups: [platform]
    no-write: true            # 他人擁有：不 commit、不 push、不 foreach

  - name: shared-proto
    url: https://git.example.com/game/shared-proto.git
    groups: [platform, proto]

  - name: drawer-tool
    url: https://git.example.com/game/tools/drawer-tool.git
    groups: [game]
```

**條目順序**：`repos` 依 `name` 的位元組序排列。`gits add` / `gits adopt` **依序插入**，不附加於尾端——這讓兩台機器各自新增 repo 時落在檔案的不同位置，git 多半能自動 merge，而附加於尾端必然造成同一位置的衝突（見 §10.1）。

### 5.3 欄位定義

| 欄位 | 型別 | 必填 | 預設 | 說明 |
| --- | --- | --- | --- | --- |
| `version` | int | 是 | — | manifest 結構版本。v1 固定為 `1`。遇到更高版本時 `gits` 應拒絕執行並提示升級。 |
| `defaults.remote` | string | 否 | `origin` | 所有 repo 的預設 remote 名稱。 |
| `defaults.branch` | string | 否 | `main` | 所有 repo 的預設分支。 |
| `repos[].name` | string | 是 | — | 唯一識別字，同時是預設目錄名稱。 |
| `repos[].url` | string | 是 | — | clone 用的 URL。 |
| `repos[].path` | string | 否 | 同 `name` | 相對於 workspace 根目錄的路徑。`"."` 代表根 repo。 |
| `repos[].branch` | string | 否 | `defaults.branch` | **語義為「預設分支」**：僅用於 `clone` 時指定 checkout 目標、`status` 判斷「目前在別的分支上」的對照基準、以及 `deps` 的比較基準（見 §7.11）。`gits` **永不自動切換分支**——在 feature 分支上工作是常態，不是錯誤。 |
| `repos[].remote` | string | 否 | `defaults.remote` | 該 repo 的 remote 名稱。 |
| `repos[].groups` | []string | 否 | `[]` | 群組標籤。 |
| `repos[].no-write` | bool | 否 | `false` | 見 §4 的定義。 |
| `repos[].description` | string | 否 | `""` | 顯示用的一行說明。 |

### 5.4 根 repo

workspace 根目錄本身經常也是一個 git repo（存放跨專案文件、`CLAUDE.md`、`gits.yaml` 自己）。若不把它納管，會出現一個直接違背痛點 2 的洞：使用者跑完 `gits status` → `gits commit` → `gits push`，**根目錄那個 repo 的改動一個字都不會被提到**。

規則：

- 根 repo 以 `path: "."` 的普通條目表示，`status`／`commit`／`push`／`sync` 一視同仁地處理。
- `gits init` 偵測到根目錄含 `.git` 時，自動寫入這筆條目。
- 掃描第一層子目錄時（`init`／`adopt`）不會把根目錄自己算成子目錄，不會重複登記。
- `gits up` 與 `gits sync` 對根 repo 有特殊的**執行順序**要求，見 §7.1 與 §7.3。

### 5.5 本地覆寫：`gits.local.yaml`

兩台機器不會完全一樣：這台機器可能刻意不拉某幾個 repo，或某個 repo 放在不同路徑。若只能改共用的 `gits.yaml`，使用者就會製造出永遠不該 commit 的本地修改。

- 檔名 `gits.local.yaml`，位於 workspace 根目錄，**必須加入 `.gitignore`**（`gits init` 會處理）。
- 只允許覆寫既有條目的少數欄位：

```yaml
version: 1
overrides:
  - name: legacy-synth
    disabled: true            # 這台機器刻意沒有它；不列入任何指令，不報 missing
  - name: stack-tools
    path: ../shared/stack-tools
  - name: drawer-tool
    no-write: true
```

| 欄位 | 說明 |
| --- | --- |
| `disabled` | 該 repo 在這台機器上完全不參與任何指令。**不會被報成 `missing`。** |
| `path` | 覆寫路徑。 |
| `no-write` | 覆寫寫入邊界（只能收緊為 `true`，不能放寬為 `false`）。 |

**不允許新增 repo 條目。** 新 repo 一律進共用清單，否則痛點 3 會從另一個方向回來。

`disabled` 之所以重要：agent 常在只 clone 了部分 repo 的環境運作（CI、sandbox、新開的 worktree）。缺少明確標記時，每次 `status` 都會報一個假的 `missing`——人會學會忽略，**agent 則會每次都試圖「修好」它**。

### 5.6 驗證規則

載入 manifest 時執行下列檢查，任一失敗即以退出碼 `2`（代號 `E_MANIFEST`）中止並指出出錯的行號：

- `version` 存在且為已支援的值。
- 每個條目具備 `name` 與 `url`。
- `name` 不重複、`path` 解析後不重複。
- `path` 不得逃出 workspace 根目錄（不允許 `..` 或絕對路徑）；`"."` 是唯一的特例。
- 至多一筆 `path: "."`。
- `gits.local.yaml` 的每個 `overrides[].name` 都能對應到 `gits.yaml` 中的條目。

---

## 6. 全域行為

### 6.1 workspace 定位

1. 若指定 `-w, --workspace <path>`，直接採用。
2. 否則若環境變數 `GITS_WORKSPACE` 存在，採用之。
3. 否則自目前工作目錄逐層向上尋找 `gits.yaml`，取最先找到的那一份（與 git 尋找 `.git` 的行為一致）。
4. 都找不到 → 退出碼 `2`（`E_NO_WORKSPACE`），訊息明確指出「cannot find gits.yaml」並建議執行 `gits init`。

### 6.2 篩選

| 旗標 | 說明 |
| --- | --- |
| `-g, --group <name>` | 只操作屬於該群組的 repo。可重複指定，多個群組取聯集。 |
| `-r, --repo <name>` | 只操作指定 repo。可重複指定。 |
| `--exclude <name>` | 排除指定 repo。可重複指定。 |

未指定任何篩選時，作用於 manifest 中的全部 repo（寫入類指令仍自動排除 `no-write`，所有指令一律排除 `disabled`）。

### 6.3 並行

- 唯讀指令（`status`、`deps`、`list`）與 `sync`、`clone`、`foreach` 並行執行，預設並行度為 `min(8, CPU 核心數)`，可用 `-j, --jobs <n>` 調整。
- 互動指令（`commit` 的互動模式）序列執行。
- **輸出一律等到結果彙整後、依 manifest 順序印出**，不允許並行輸出交錯。這同時保證了輸出的決定性（§6.5）。

### 6.4 兩種輸出模式

**介面與輸出一律使用英文**：`gits` 所有的文字輸出——人類模式的狀態行與摘要、確認提示、互動模式的提示字元、JSON 模式的 `message`／`hint` 欄位——固定使用英文，不隨系統語系（locale）切換，也不提供多語系選項。理由有二：一是 `message`／`hint` 是 agent 賴以比對與取得下一步指令的欄位，字串若隨機器語系漂移，比對邏輯就會跟著碎裂；二是輸出經常被貼到 issue、log、或分享給不同語言的協作者，固定英文才不失真。此規則只約束 `gits` 自己產生的文字；manifest 中使用者自訂的欄位（例如 `description`）是使用者自己的資料，不受此限，可以是任何語言（見 §5.2 範例）。

**人類模式（預設）**

- 彩色，狀態以簡短符號表示：`✓` 正常、`●` 有未提交改動、`↑` 領先、`↓` 落後、`⚠` 需注意、`✗` 失敗、`?` 資料不足。
- 偵測到非 TTY、或設定 `NO_COLOR` 環境變數時，降級為純 ASCII 無色輸出。

**機器模式（`--json`）**

- **每一個指令都支援 `--json`**，包括 `commit`、`push`、`clone`、`add`、`adopt`、`init`、`foreach` 的執行結果，不只查詢類指令。
- **stdout 只有一個 JSON 物件**，沒有別的。所有進度、警告、`-v` 的 git 指令輸出、確認提示，一律走 **stderr**。這樣 `gits status --json | jq` 才會可靠。
- 隱含 `--plain`：不輸出任何裝飾性文字或 ANSI 序列。

### 6.5 JSON 輸出契約

```json
{
  "schemaVersion": 1,
  "command": "status",
  "workspace": "C:/Users/gravity/Documents/Repositories/example-workspace",
  "manifestPath": "C:/Users/gravity/Documents/Repositories/example-workspace/gits.yaml",
  "network": false,
  "stale": true,
  "repos": [
    {
      "name": "drawer-tool",
      "path": "drawer-tool",
      "groups": ["game"],
      "state": "behind",
      "exists": true,
      "branch": "main",
      "defaultBranch": "main",
      "onDefaultBranch": true,
      "upstream": "origin/main",
      "ahead": 0,
      "behind": 2,
      "dirty": { "tracked": 0, "untracked": 1 },
      "submodulesClean": true,
      "noWrite": false
    },
    {
      "name": "stack-tools",
      "path": "tools/stack-tools",
      "state": "missing",
      "exists": false,
      "code": "E_MISSING_DIR",
      "message": "directory does not exist",
      "hint": "gits clone -r stack-tools"
    }
  ],
  "summary": {
    "total": 18, "clean": 14, "dirty": 2, "behind": 1,
    "missing": 1, "failed": 0, "skipped": 0
  },
  "deps": { "outdated": 3, "diverged": 1 }
}
```

規則：

1. **每個 repo 都有一個單一的 `state` 列舉**，agent 可直接分支，不必自己從五個布林值推導。原始欄位（`ahead`／`behind`／`dirty`）永遠同時存在，所以完整資訊不會遺失。

   `state` 的值：`clean`｜`dirty`｜`ahead`｜`behind`｜`diverged`｜`detached`｜`no-upstream`｜`missing`｜`not-a-repo`｜`error`

   一個 repo 可能同時髒又落後。`state` 取**最需要人處理的那一項**，優先序由高到低固定為：

   `error` > `not-a-repo` > `missing` > `detached` > `no-upstream` > `diverged` > `dirty` > `behind` > `ahead` > `clean`

2. **輸出必須有決定性。** 欄位順序固定、repo 順序照 manifest、**不含時間戳與耗時**（`-v` 時才寫入 stderr）。前後跑兩次做 diff 是常見用法，任何噪音都會讓 diff 失效。

3. **省略預設值與空值。** 18 個 repo 的完整 `status --json` 約 4–6KB，對 agent 的 context 可接受；`deps --json` 預設只輸出摘要層級，`-v` 才展開每個 commit 的細節。

4. `schemaVersion` 在任何不相容變更時遞增。

### 6.6 錯誤代號

失敗與跳過一律附帶穩定的代號，而非只有散文。JSON 中放在 repo 條目的 `code`；人類模式下印在原因欄位的括號裡。

| 代號 | 意義 | 該重試嗎 |
| --- | --- | --- |
| `E_DIRTY` | 有未提交改動，操作已跳過 | 否 |
| `E_DIVERGED` | 同時領先與落後 | 否 |
| `E_DETACHED` | detached HEAD | 否 |
| `E_NO_UPSTREAM` | 分支沒有 upstream | 否 |
| `E_MISSING_DIR` | manifest 有記載但目錄不存在 | 否（先 `gits clone`） |
| `E_NOT_A_REPO` | 目錄存在但不是 git repo | 否 |
| `E_NO_WRITE` | 該 repo 標為 `no-write`，寫入類操作已排除 | 否 |
| `E_AUTH` | 認證失敗（無憑證、權限不足） | **否——重試一百次也不會成功** |
| `E_NETWORK` | 網路錯誤（DNS、連線逾時、遠端 5xx） | **是** |
| `E_TIMEOUT` | git 子行程超過 `--timeout` | 是（或加大 timeout） |
| `E_HOOK_FAILED` | git hook 以非零碼結束 | 否 |
| `E_NO_CANONICAL` | `deps` 在 workspace 中找不到本尊，判定不完整 | 否 |
| `E_MANIFEST` | manifest 格式錯誤 | 否 |
| `E_NO_WORKSPACE` | 找不到 `gits.yaml` | 否 |
| `E_NEEDS_YES` | 非互動環境需要 `--yes` | 否 |
| `E_MAX_REPOS` | 計畫影響的 repo 數超過 `--max-repos` | 否 |

`E_AUTH` 與 `E_NETWORK` 必須確實分開：**網路錯誤該重試，認證錯誤重試只會把 agent 的預算燒光。**

每個失敗條目應盡量附上 `hint`——一句「人或 agent 下一步能直接貼上執行的指令」。成本極低，對兩種使用者都有用。

### 6.7 非互動語義

這是 agent 最容易踩到、後果最嚴重的一組行為。

1. **偵測到 stdin 非 TTY 且未給 `-y`，且該指令需要確認時 → 立刻以退出碼 `2`（`E_NEEDS_YES`）結束**，訊息為「non-interactive environment requires --yes」。**永遠不要在非 TTY 上等待輸入**——那會變成一個沒有輸出、不會結束的行程，通常要等到呼叫端 timeout 才被殺掉，而且不留任何線索。
2. `gits commit` 的互動模式在非 TTY 下**直接報錯**要求改用 `-m`，不進入提示迴圈。
3. `-y` 對確認的略過是全指令一致的；`--dry-run` 在任何模式下都不需要 `-y`。
4. 環境變數 `GITS_YES=1` 等同 `-y`，方便在 CI 與 agent 環境統一設定。

### 6.8 git 子行程的執行環境

`gits` 自己不阻塞還不夠——它呼叫的 `git` 會自己跳出來要輸入。在沒有 credential helper 的環境（容器、CI、剛裝好的另一台機器）跑 `gits sync`，`git fetch` 會停在 `Username for 'https://...':` 等到天荒地老。

**所有 git 子行程一律以下列環境執行：**

```
GIT_TERMINAL_PROMPT=0                    # 禁止 git 向終端要憑證，改為直接失敗
GIT_ASKPASS=                             # 清空，避免彈出 GUI 憑證對話框
SSH_ASKPASS=
GIT_SSH_COMMAND="ssh -o BatchMode=yes"   # SSH 不互動（未被使用者覆寫時）
GIT_OPTIONAL_LOCKS=0                     # 唯讀操作不去搶 index.lock
```

並以 `-c` 傳入 `core.pager=cat`、`color.ui=false`，確保輸出可解析。

**逾時**：每個 git 子行程受 `--timeout <duration>`（預設 `120s`）約束，超時後 kill 整個 process group 並記為 `E_TIMEOUT`。pre-commit hook 卡住是常見情況，沒有逾時就等於沒有上限。

**hooks 一律照跑，不繞過。** `gits` 不提供 `--no-verify`；hook 失敗記為 `E_HOOK_FAILED`。簽章、editor、credential helper 全部沿用該 repo 既有設定。

### 6.9 網路策略

| 指令 | 預設是否連網 |
| --- | --- |
| `status` / `deps` / `list` | 否（僅讀本機 refs），`--fetch` 才連網 |
| `sync` / `clone` / `push` / `up` | 是（本質上就是網路操作） |
| `commit` / `adopt` / `add` / `init` | 否 |
| `foreach` | 取決於使用者給的指令 |

**關於離線 status 的準確度**：不連網時的 ahead/behind 是相對於本機 remote-tracking ref，可能過期。`gits` 的處理很單純——**照實說**：報告結尾（JSON 中為 `"stale": true`）註明「data may be stale (offline); add --fetch for live status」。不做任何猜測性的補救。

### 6.10 退出碼

| 碼 | 意義 |
| --- | --- |
| `0` | 全部成功 |
| `1` | 至少一個 repo 的操作**失敗** |
| `2` | 使用錯誤（manifest 找不到／格式錯誤／參數錯誤／非互動缺 `--yes`） |
| `3` | 沒有失敗，但**有狀況**（僅在指定 `--exit-code` 時回傳） |
| `130` | 使用者中斷 |

`1` 與 `3` 刻意分開：「有 repo 操作失敗」與「一切正常但有東西落後了」對呼叫者的處置完全不同。這與 `git diff --exit-code` 只用 `1` 的慣例不同，正確性優先。

`status`、`deps`、`list` 這類回報型指令若未指定 `--exit-code`，即使發現問題也回傳 `0`。

### 6.11 冪等性

Agent 會重試，所以哪些指令重跑安全必須明確承諾：

| 指令 | 重跑行為 |
| --- | --- |
| `status` / `deps` / `list` | 純唯讀，永遠安全 |
| `sync` / `up` / `clone` / `push` | 冪等；已是最新／已存在／已推送則 no-op |
| `commit` | 無改動時 no-op，因此重試安全（不會產生空 commit） |
| `add` / `adopt` | 條目已存在時 no-op（退出碼 `0`） |
| `init` | `gits.yaml` 已存在時**報錯，不覆寫**（退出碼 `2`） |
| `foreach` | 取決於使用者給的指令，`gits` 不做保證 |

### 6.12 其他全域旗標

| 旗標 | 說明 |
| --- | --- |
| `-y, --yes` | 略過確認提示。非互動環境必須明示。 |
| `-n, --dry-run` | 只印出將執行的動作，不實際執行。**所有寫入類指令一律支援。** |
| `--max-repos <n>` | 計畫影響的 repo 數超過 `n` 時拒絕執行（`E_MAX_REPOS`）。錯誤往往是「範圍炸開」而非「單點做錯」，這條擋得住。 |
| `--json` | 機器可讀輸出，見 §6.4／§6.5。 |
| `--timeout <duration>` | 單一 git 子行程逾時，預設 `120s`。 |
| `-j, --jobs <n>` | 並行度。 |
| `-v, --verbose` | 在 stderr 顯示實際執行的 git 指令與其輸出。 |
| `--plain` | 強制純 ASCII 無色輸出（`--json` 隱含此項）。 |
| `--version` / `-h, --help` | 標準行為。 |

---

## 7. 指令規格

### 7.0 指令總覽

v1 提供十二個指令，但**日常只需要記兩個動詞**：

| 分類 | 指令 | 用途 |
| --- | --- | --- |
| **日常** | `up`、`status` | 「把我拉到最新」與「現在怎麼樣」。九成的使用只會用到這兩個。 |
| 寫入 | `commit`、`push` | 收工前的提交與推送。 |
| 清單維護 | `init`、`adopt`、`add`、`clone` | 建立清單、登記新 repo、補齊缺少的 repo。 |
| 查詢 | `list`、`deps` | 給 agent 與腳本的結構化查詢。 |
| 原子操作 | `sync` | `up` 的其中一段，需要精準控制時使用。 |
| 逃生口 | `foreach` | `gits` 沒封裝的事，直接批次執行 git 指令。 |

`up` 是把 `clone` + `sync` + `status` + `deps` 串起來的組合指令；其餘是原子指令，供腳本與精準操作使用。這樣使用者不需要記住「`sync` 不會補齊缺少的 repo」這種內部切分。

### 7.1 `gits up`

**日常主要動詞：把整個 workspace 拉到最新，然後告訴我現在怎麼樣。**

執行順序（順序本身是規格的一部分）：

1. **先同步根 repo**（若存在）：`git fetch --prune` 後嘗試 fast-forward。
2. **重新載入 `gits.yaml`**——repo 清單可能剛剛才變了。
   - 若根 repo 因未提交改動或分歧而無法 ff → **明確警告「repo list may be stale」**（`E_DIRTY` / `E_DIVERGED`），然後用舊清單繼續，絕不靜默。
3. **clone 缺少的 repo**（等同 `gits clone`，可用 `--no-clone` 關閉）。
4. **同步其餘 repo**（等同 `gits sync`）。
5. 印出 `status` 摘要，並附上 `deps` 的一行摘要。

第 1–2 步是痛點 3 能否真正解掉的關鍵：manifest 進了根 repo 之後，根 repo 就成了必須「先」同步的東西。若跳過，昨晚在家新增的 repo 今天在公司永遠不會出現——因為記載它的那份 `gits.yaml` 自己還沒被拉下來。

| 旗標 | 說明 |
| --- | --- |
| `--no-clone` | 不補齊缺少的 repo。 |
| `--no-submodules` | 跳過 submodule 更新。 |
| `-n, --dry-run` | 只 fetch 並報告會做什麼。 |
| `--json` | 結構化輸出（含各階段結果）。 |

### 7.2 `gits status`

一次看完整組 repo 的狀態。**這是最常用的查詢指令，也是所有其他指令的判斷基礎。**

**預設不連網。** 對每個 repo 蒐集：目前分支、是否偏離 manifest 的預設分支、未提交改動的檔案數（已追蹤／未追蹤分別計算）、相對 upstream 的 ahead／behind、submodule 工作區是否與 gitlink 一致、是否為 `no-write`、目錄是否存在。

**輸出：全部列出，正常的淡化為一行帶過，有狀況的高亮。** 這樣使用者一眼就知道「全部都掃過了」，而不是懷疑工具是否漏掉某個 repo。

**預設不依群組分組**，照 manifest 順序印出——因為 repo 可多重歸屬群組（例如 `shared-proto` 同時屬於 `platform` 與 `proto`），分組會造成重複列出或需要武斷地挑一個。要分組時用 `--by-group`，該模式下允許同一個 repo 出現在多個群組底下。

```
workspace: C:\Users\gravity\Documents\Repositories\example-workspace  (18 repos)

  ✓ .                                main       (workspace root)
  ✓ game-server   main
  ↓ drawer-tool                  main       behind 2
  ● client-cli              main       uncommitted 1 (docker/compose.yaml)
  ● vendor-sdk         main       uncommitted 1 (go.mod)         [no-write]
  ⚠ shared-proto                  feature/arcade-proto   (default is main)
  ✗ stack-tools                   —          directory does not exist (E_MISSING_DIR)
    → gits clone -r stack-tools
  ...

summary: 18 repos — 14 clean, 2 dirty, 1 behind, 1 missing
deps: 3 repos pinned to an outdated shared-proto, 1 diverged (see gits deps for details)
data may be stale (offline); add --fetch for live status
```

**`deps` 摘要接在 `status` 尾巴是刻意的**：痛點 4 的本質是「看不見」，而使用者不會主動去跑一個他忘記存在的指令。讓它出現在每天都會跑的指令裡。可用 `--no-deps` 關閉（大型 workspace 若嫌慢）。

**`no-write` repo 的髒狀態降級顯示**：這類 repo 你可能會做本地實驗改動，但永遠不會提交。它們的 `●` 不計入摘要的「有未提交改動」數字，避免變成永久噪音。

| 旗標 | 說明 |
| --- | --- |
| `--fetch` | 先對每個 repo 執行 `git fetch --prune`，再計算狀態。 |
| `--by-group` | 依群組分組列出（同一 repo 可重複出現）。 |
| `--no-deps` | 不計算尾端的依賴摘要。 |
| `--exit-code` | 有任何狀況時回傳退出碼 `3`。 |
| `--json` | 見 §6.5。 |

`⚠` 分支偏離**只是標示，不是錯誤**——在 feature 分支上工作是常態。

### 7.3 `gits sync`

把整組 repo 拉到最新。策略保守：**絕不碰未提交的改動，絕不製造衝突。**

若 manifest 含根 repo，**根 repo 先同步、然後重新載入 manifest**，再處理其餘 repo（理由同 §7.1）。

對每個 repo（**包含 `no-write`**，因為拉取對遠端而言是唯讀操作）：

1. `git fetch --prune <remote>`。
2. 判斷本地狀態，依下表處置：

| 狀態 | 動作 | 代號 |
| --- | --- | --- |
| 有未提交改動（已追蹤檔案被修改） | **跳過**，列入報告 | `E_DIRTY` |
| detached HEAD | **跳過**，列入報告 | `E_DETACHED` |
| 無 upstream | **跳過**，列入報告 | `E_NO_UPSTREAM` |
| 已是最新 | 不動 | — |
| 純落後（可 fast-forward） | 執行 `git merge --ff-only` | — |
| 純領先 | 不動；提示可用 `gits push` | — |
| 分歧（同時領先且落後） | **跳過**，標示需人工處理 | `E_DIVERGED` |
| 目錄不存在 | **跳過**；提示執行 `gits clone` 補齊 | `E_MISSING_DIR` |

3. 成功 fast-forward 之後，若該 repo 有 `.gitmodules`，執行 `git submodule update --init --recursive`。
   > 這是預設行為而非選項：不做的話，`proto/` 這類 submodule 的工作區會停留在舊 SHA，導致建置結果與 gitlink 不一致——這是實際會踩到的坑。可用 `--no-submodules` 關閉。

4. 全部完成後印出摘要：更新了哪幾個（各前進幾個 commit）、跳過了哪幾個及原因、失敗了哪幾個。**每個跳過的 repo 都附一句可直接執行的下一步**（例如分歧時給出 `cd <repo> && git rebase origin/main`），而不是只說「需人工處理」。

**`sync` 不會 clone 缺少的 repo，也不會 push。** 需要一次做完請用 `gits up`。

| 旗標 | 說明 |
| --- | --- |
| `--no-submodules` | 跳過 submodule 更新。 |
| `-n, --dry-run` | 只 fetch 並報告會做什麼，不實際 merge。 |
| `--json` | 結構化輸出。 |

### 7.4 `gits push`

把所有領先的 repo 推上去。

- **自動排除 `no-write` repo。**
- 只處理 ahead > 0 的 repo。
- 拒絕推送（列入報告但不執行）的情況：分歧（`E_DIVERGED`）、detached HEAD（`E_DETACHED`）、無 upstream（`E_NO_UPSTREAM`）。
  - 無 upstream 時，若使用者已設定 `push.autoSetupRemote` 或指定 `--set-upstream`，則改為以 `-u` 推送。
- **v1 不提供任何形式的 force push。** 需要時請自行 `cd` 進該 repo 操作。
- **預設先列出計畫並要求確認**（推送是對外可見的動作）：

```
Will push 2 repos:
  drawer-tool                 main → origin/main    3 commits
  arcade-server              main → origin/main    1 commit

Skipped:
  vendor-sdk        no-write
  client-cli             diverged (ahead 1, behind 2) (E_DIVERGED)
                                  → cd client-cli && git rebase origin/main

Continue? [y/N]
```

`-y` 略過確認；非互動環境未給 `-y` 時直接以 `E_NEEDS_YES` 失敗（§6.7）。建議搭配 `--max-repos` 為自動化情境設一個上限。

### 7.5 `gits commit`

對所有有未提交改動的 repo 逐一提交。

**共通行為**

- **自動排除 `no-write` repo。**
- **預設只提交已追蹤檔案的改動**（等同 `git commit -a` 的語義）。未追蹤檔案會在摘要中列出提醒，但不會被加入——避免誤將本機設定檔或建置產物提交進去。加 `-A, --all` 才執行 `git add -A`。
- 簽章、hooks、editor 全部沿用該 repo 既有的 git 設定，`gits` 不介入、不覆寫、不繞過。
- **`commit` 不會自動 push。**

**互動模式（預設，僅限 TTY）**

序列處理每個有改動的 repo：

```
[1/3] drawer-tool  (main)
  M  internal/round/loop.go
  M  internal/round/timing.go
  ?? notes.txt                       (untracked, will not be committed)

message > _
```

| 輸入 | 行為 |
| --- | --- |
| 一行文字 | 以該訊息提交 |
| 空行 | 跳過此 repo |
| `d` | 顯示 `git diff --stat` |
| `dd` | 顯示完整 diff |
| `e` | 開啟編輯器撰寫訊息（支援多行） |
| `q` | 中止，不處理剩餘 repo（已提交的保留） |

**非互動環境下不進入此模式**，直接以 `E_NEEDS_YES` 報錯並提示改用 `-m`。

**快速路徑**

`gits commit -m "訊息"` 對所有有改動的 repo 套用同一則訊息。此模式仍會先列出將提交的 repo 與檔案數並要求確認，`-y` 可略過。

**逐 repo 不同訊息（agent 常用）**：搭配 `-r` 篩選重複呼叫即可，每次都是一個乾淨的非互動操作：

```
gits commit -r drawer-tool -m "fix: round timing drift" -y --json
gits commit -r arcade-server -m "feat: seat state sync" -y --json
```

| 旗標 | 說明 |
| --- | --- |
| `-m <msg>` | 快速路徑，同一則訊息套用全部（經篩選後）的 repo。 |
| `-A, --all` | 一併加入未追蹤檔案。 |
| `-y, --yes` | 略過 `-m` 模式的確認。 |
| `--json` | 結構化輸出（含每個 repo 的新 commit SHA）。 |

### 7.6 `gits clone`

補齊 manifest 中有、但本機不存在的 repo。**這是「換電腦接續工作」的關鍵指令。**

- 計算差集：manifest 有 × 本機無（`disabled` 的不算）。
- 先列出將 clone 的清單並要求確認（`-y` 略過）。
- 逐一 `git clone --branch <branch> <url> <path>`，並行執行。
- clone 完成後，若有 `.gitmodules` 則執行 `git submodule update --init --recursive`。
- 目錄已存在但不是 git repo → 跳過並警告（`E_NOT_A_REPO`），不覆寫。
- 個別失敗不中斷其他；結尾列出失敗清單與代號（`E_AUTH` 與 `E_NETWORK` 分開，見 §6.6）。

| 旗標 | 說明 |
| --- | --- |
| `-g, --group <name>` | 只補齊指定群組。 |
| `--no-submodules` | 不初始化 submodule。 |
| `--json` | 結構化輸出。 |

### 7.7 `gits init`

在目前目錄建立 manifest 骨架。

- 若 `gits.yaml` 已存在 → 錯誤，不覆寫（退出碼 `2`）。
- **偵測根目錄是否含 `.git`**，若是則寫入 `path: "."` 的根 repo 條目（§5.4）。
- 掃描第一層子目錄中所有含 `.git` 的目錄，連同其 `origin` URL 與目前分支寫入 manifest（此步驟直接沿用 `adopt` 的邏輯，不另寫一份）。
- 無法判定 URL 的 repo 仍寫入，但 `url` 留空並附上待補註解。
- `no-write` 與 `groups` 一律留空，由使用者自行標註——**工具不猜測所有權**。
- **檢查 `gits.yaml` 是否會被 `.gitignore` 排除**（`git check-ignore -q gits.yaml`）。若會，詢問是否自動加入白名單並提示 `git add gits.yaml`。
  > 這一步不做，使用者會很開心地跑完 `gits init`，然後在另一台機器發現什麼都沒同步過去。採用「`*` 加白名單」型 `.gitignore` 的 workspace 尤其會踩到。
- 將 `gits.local.yaml` 加入 `.gitignore`。
- 完成後印出「wrote N entries, please fill in groups and no-write」的提示。

### 7.8 `gits adopt`

把本機已存在、但 manifest 沒記載的 repo 登記進去。與 `clone` 互為反向。

- 掃描 workspace 第一層子目錄，找出含 `.git` 但不在 manifest 的目錄（根目錄自己不算，見 §5.4）。
- 逐一互動詢問：是否納入、歸屬哪些群組、是否 `no-write`。
- **`-y` 表示全部納入、套用旗標指定的 group／no-write、不問任何問題**，讓這個指令在非互動環境下完全可用。
- 自該 repo 讀取 `origin` URL 與目前分支填入條目（分支與 `defaults.branch` 相同時省略不寫）。
- 寫回 manifest 時保留既有註解，新條目**依 `name` 排序插入**（§5.2）。
- 同時檢查兩種不一致並回報（**只回報，不自動修改**）：
  - manifest 有記載、本機不存在 → 提示可用 `gits clone` 補齊。
  - 本機存在但 `origin` URL 與 manifest 不符 → 警告可能指向了錯誤的來源。

| 旗標 | 說明 |
| --- | --- |
| `-n, --dry-run` | 只顯示會新增什麼，不寫檔。 |
| `-y, --yes` | 全部納入，不逐一詢問。 |
| `--group <name>` | 新條目一律歸入此群組。 |
| `--no-write` | 新條目一律標記為 `no-write`。 |
| `--json` | 結構化輸出。 |

### 7.9 `gits add`

登記單一 repo 條目。零互動、可腳本化——**這是 agent 與腳本新增 repo 的正式入口**，避免任何人直接編輯 `gits.yaml`（§5.1）。

```
gits add <name> --url <url> [--path <p>] [--branch <b>] [--group <g>]... [--no-write] [--description <d>]
```

- 條目已存在且內容相同 → no-op，退出碼 `0`。
- 條目已存在但內容不同 → 錯誤，除非指定 `--update`。
- 依 `name` 排序插入，保留既有註解。
- 只寫 manifest，**不 clone**——需要實體時再跑 `gits clone -r <name>`。

### 7.10 `gits list`

列出 manifest 定義的 repo。**純粹讀 manifest，不碰檔案系統，毫秒級。**

這是 agent 回答「這個 workspace 有哪些 repo、在哪、誰是唯讀」的最省成本入口——比 `status` 便宜，也比讓 agent 自己 parse YAML 穩定。

```
gits list --json
gits list -g game --json
gits list --format=markdown
```

| 旗標 | 說明 |
| --- | --- |
| `--json` | 結構化輸出（name／path／url／branch／groups／noWrite／description）。 |
| `--format=markdown` | 輸出 Markdown 表格，可直接貼進 `CLAUDE.md`／README。 |
| `--names` | 只輸出名稱，一行一個，方便 shell 迴圈。 |

`--format=markdown` 是給痛點 5 的直接解法：把 `CLAUDE.md` 裡那張手寫的 repo 表格改成生成的（例如夾在 `<!-- gits:begin -->` / `<!-- gits:end -->` 之間），漂移就從根本上消失了。

### 7.11 `gits deps`

回報跨 repo 的 submodule 相依狀態。**v1 的依賴資訊完全由既有的 git 中繼資料推導，不需要任何額外宣告。**

**資料來源**

對每個 repo，讀取 `.gitmodules` 取得 submodule 的路徑、URL、宣告的分支，再從 `HEAD` 的 tree 讀出該路徑的 gitlink SHA（即「這個 repo 釘住依賴的哪個 commit」）。

**本尊解析**

將每個 submodule URL 正規化後與 workspace 中各 repo 的 URL 比對。正規化須忽略 scheme、使用者名稱、連接埠、結尾的 `.git`——這不是防禦性設計，是必要條件：實測同一個 `shared-proto` 在 9 個 repo 的 `.gitmodules` 裡出現三種寫法（`ssh://git@host:24/a/b.git`、`https://host/a/b.git`、`https://host/a/b`）。

submodule 的**路徑名稱不可作為配對依據**（實測 8 個叫 `proto`、1 個叫 `shared-proto`），只能靠 URL。

找到對應的 repo 時，**該 checkout 即為本尊**，用它的 commit graph 當權威時間軸。

**比較基準**

基準分支的優先序，由高到低：

1. 依賴方 `.gitmodules` 中的 `submodule.<name>.branch`——**這是該 repo 自己宣告過的意圖**。
2. 本尊在 manifest 中的 `branch`。
3. `defaults.branch`。

比較對象一律是本尊的 `refs/remotes/<remote>/<branch>`，**絕不使用本尊當前的 HEAD 或當前分支**——本尊很可能正 checkout 在某條 feature 分支上（實測 `shared-proto` 就是），用 HEAD 會讓同一個 workspace 在兩台機器上給出不同結論。

> 為什麼基準要逐個依賴方決定：實測 `arcade-client-cli` 在 `.gitmodules` 裡宣告 `branch = feature/arcade-proto`，若用統一的 `main` 當基準，它會**永遠**掛著一個警告——但它釘在自己宣告的分支上，那是刻意的。永遠亮著的警告會讓使用者三天內學會無視所有警告，`deps` 的價值就歸零了。

**判定（有本尊時，完全離線）**

對每個釘住的 SHA，先用 `git merge-base --is-ancestor` 判斷祖先關係，再用 `git rev-list --left-right --count <sha>...<base>` 取得雙向計數：

| 判定 | 依據 |
| --- | --- |
| 最新 | SHA 即為基準分支的 HEAD |
| 落後 N 個 commit | SHA 是基準的祖先，雙向計數為 `0 / N` |
| 領先 N 個 commit | 基準是 SHA 的祖先，雙向計數為 `N / 0` |
| **分歧（領先 N、落後 M）** | 互不為祖先，雙向計數為 `N / M`。附上 `git branch --contains <sha>` 找到的分支名稱作為提示。 |
| 未知 | 該 SHA 在本尊中不存在——本尊可能需要 fetch。提示加 `--fetch` |

> **祖先判斷不可省略。** `git rev-list --count <sha>..<base>` 對非祖先的 commit 一樣會回傳數字。實測 `arcade-client-cli` 釘的 SHA 用該公式得到 `3`，看起來是「behind 3」，實際是「ahead 3, behind 3」的分歧。使用者看到「behind 3」會以為 `git submodule update --remote` 就能解決，但那是條岔出去的線。

**判定（無本尊時）**

僅比較各 repo 釘的 SHA 是否一致，回報「N repos pinned to M different SHAs」。**JSON 中明確輸出 `"canonical": null` 與 `"code": "E_NO_CANONICAL"`**，並附上 hint「add `<name>` to the workspace for a complete determination」。

呼叫者（尤其是 agent）需要知道「這份答案不完整」，勝過拿到一份看起來完整的答案。v1 不透過 `git ls-remote` 做降級判定——那是第二套邏輯，收益不足以支撐維護成本。

**輸出：以「被依賴者」分組**

```
shared-proto  (canonical: ./shared-proto, baseline origin/main @ a1b2c3d)

  ✓ arcade-server                 a1b2c3d   up to date
  ↓ game-server     ca3426c   behind 3
  ↓ drawer-tool                    ca3426c   behind 3
  ↓ drawer                             8de0f2a   behind 18
  ⚠ arcade-client-cli                   d2b1fb2   diverged: ahead 3, behind 3
                                                 (baseline is its declared feature/arcade-proto)
  ? stress-tool                     0011223   commit not found in canonical, try --fetch

9 repos depend on shared-proto, pinned to 7 different commits
```

**明確的非承諾**：`deps` 回報的是「落後／分歧／不一致」這些**事實**，不宣稱依賴一定被破壞。判斷落後 3 個 commit 是否真的影響某個 repo，需要人（或 agent）去看那 3 個 commit 改了什麼。自動化的影響分析屬於 v2（見 §11）。

| 旗標 | 說明 |
| --- | --- |
| `--fetch` | 先更新本尊的 remote refs 再比對。 |
| `--exit-code` | 發現任何落後或分歧時回傳退出碼 `3`。 |
| `--json` | 輸出依賴圖摘要；`-v` 展開每個 commit 的細節。 |

`deps --json` 是 `gits` 對 agent 最有價值的單一輸出：要 agent 自己去 parse 9 份 `.gitmodules`、正規化三種 URL 寫法、跑 `merge-base` 與雙向計數，成本高且極易出錯。

### 7.12 `gits foreach`

對全部或篩選後的 repo 執行任意 git 指令。這是 `gits` 沒有封裝的所有事情的逃生口。

```
gits foreach -- git submodule update --remote proto
gits foreach -g game --json -- git log --oneline -1
```

- **預設排除 `no-write` repo**：指令內容對 `gits` 是不透明的，無法判斷是讀還是寫，因此一律當作寫入處理。要包含須明示 `--include-no-write`。
- 並行執行，受 `-j` 與 `--timeout` 約束。
- 執行前列出將影響的 repo 並要求確認（`-y` 略過，`--max-repos` 可設上限）。
- `--json` 為每個 repo 輸出 `exitCode` / `stdout` / `stderr`，各自截斷至 8KB 並標記 `"truncated": true`——避免一次呼叫就撐爆呼叫者的可用容量。
- 退出碼：任一 repo 非零 → `1`。

> `foreach` 對人類是 nice-to-have，對 agent 是組合能力的基礎。沒有它，agent 只能自己組 18 條 `cd X && git ...`，繞過 `gits` 的全部錯誤處理與彙整。實作成本低（平行、篩選、彙整都已存在），因此列入 v1。

---

## 8. 情境走查

以 `example-workspace` workspace 的真實狀態驗證設計是否解決了 §1 的五個痛點。

**情境一：早上到公司，接續昨晚在家的工作**

```
gits up
```

一個指令完成：先同步根 repo 取得最新的 `gits.yaml` → 補齊昨晚在家新增的 `stack-tools` → 全部拉到最新（有未提交改動的自動跳過並說明）→ 印出狀態與依賴摘要。

→ 解決痛點 1（逐一 pull）與痛點 3（repo 清單漂移）。

**情境二：一個改動橫跨三個 repo，收工前確認**

```
gits status          # 確認只有預期的那三個是髒的（含 workspace 根的文件改動）
gits commit          # 逐一看變更、各自寫訊息；no-write 的 repo 完全不會被問到
gits push            # 列出將推送什麼、要我確認、推上去
```

→ 解決痛點 2（不知道漏了什麼）。

**情境三：改了 `shared-proto` 之後**

```
gits deps            # 立刻看到哪 9 個 repo 釘了舊 SHA、誰是真的分歧
```

→ 解決痛點 4（跨 repo 版本相依看不見）。

**情境四：AI agent 接手這個 workspace**

```
gits list --json           # 這裡到底有哪些 repo、在哪、誰是唯讀 —— 不再依賴散文文件
gits status --json         # 現在哪個髒、哪個落後、哪個根本不存在
...（改動程式碼）...
gits status --json         # 確認只動了預期的檔案
gits commit -r <repo> -m "..." -y --json    # 逐 repo 提交，每次都是乾淨的非互動操作
```

→ 解決痛點 5（agent 讀到假地圖）。

**情境五：agent 執行跨 repo 的 proto 升級**

```
gits deps --json                                        # 找出釘了舊 SHA 的 repo
gits foreach -r a -r b -r c --json -- git submodule update --remote proto
gits status --json                                      # 確認結果符合預期
gits commit -m "chore: bump shared-proto" -y --json
```

→ 展示 `deps --json` + `foreach --json` 的組合價值：這是 agent 靠自己幾乎做不到的事。

---

## 9. 非目標

明確不做的事，避免範圍蔓延：

- **不取代 git。** 不做 branch／merge／rebase／stash／tag 的封裝。
- **不做 monorepo 遷移**，也不建議把 workspace 變成 superproject。
- **不管理 submodule 的生命週期。** 只讀取 `.gitmodules` 與 gitlink 作為依賴資訊來源，不新增、不移除、不改動 submodule 設定（唯一例外是 `sync`／`clone` 後的 `submodule update`，那是為了讓工作區與 gitlink 一致）。
- **不支援 git 以外的版本控制系統。**
- **不做建置、測試、部署編排。** 那是 workspace 自身工具的職責。
- **不整合 GitHub／GitLab API**（v1 範圍內；見 §11）。
- **不提供 force push、不提供自動衝突解決、不提供 `--no-verify`。**
- **不提供「只擋 agent、不擋人」的策略設定。** `gits` 無法可靠辨識呼叫者——任何 `--agent` 之類的旗標，agent 都可以不帶。所有邊界（`no-write`、`--max-repos`）一律對所有呼叫者生效。想要真正的差別授權，該用 git 伺服器端的權限，而不是本地工具的榮譽制。
- **v1 不做 MCP server。** 見 §10.2。

---

## 10. 技術決策摘要

| 項目 | 決策 | 理由 |
| --- | --- | --- |
| 實作語言 | Go | 單一執行檔、跨平台交叉編譯簡單、啟動快；`go install github.com/nekogravitycat/gits@latest` 即可在兩台機器各自安裝。 |
| manifest 格式 | YAML | 與 tsrc／vcstool／mani 同族，日後互轉容易；可寫註解（JSON 不行，而 manifest 正是需要註記「為何此 repo 為 no-write」的地方）。 |
| YAML 函式庫 | `gopkg.in/yaml.v3` | 需要節點層級 API 以在寫回時保留註解。 |
| git 操作方式 | 呼叫 `git` 執行檔 | 不使用 go-git 等純 Go 實作：確保與使用者的 git 設定（credential helper、GPG 簽章程式路徑、hooks、includeIf）行為完全一致。 |
| git 狀態解析 | `git status --porcelain=v2 --branch` | 穩定的機器介面，一次取得分支、upstream、ahead/behind 與檔案狀態。 |
| workspace 定位 | 自 cwd 向上搜尋 `gits.yaml` | 與 git 尋找 `.git` 的直覺一致，零設定。 |
| 互動介面 | 純 stdin/stdout prompt，不使用 TUI 框架 | 在 Windows Terminal、Git Bash、SSH、CI 中行為一致，且不引入大型依賴。 |
| 依賴資訊來源 | `.gitmodules` + gitlink SHA | 依賴表已經存在，不必發明新格式，也不必人工維護。 |
| agent 介面 | CLI + `--json`，不做 MCP | 見 §10.2。 |
| 相依套件 | 僅 YAML 函式庫，以及命令列解析（標準庫 `flag` 或輕量套件） | 保持依賴樹極小。 |

### 10.1 manifest 的保存與版本控制

這是痛點 3 的解法核心，也是最容易做錯的一環。

**存放位置**：`gits.yaml` 一律放在 workspace 根目錄，並**納入該 workspace 根 repo 的版本控制**。repo 清單因此得以隨版控同步到另一台機器。

**三種情況**

| 情況 | 做法 |
| --- | --- |
| 根目錄已是 git repo（`example-workspace` 即是，remote 為 `workspace.git`） | 直接 `git add gits.yaml` 並 commit。`gits init` 會自動寫入 `path: "."` 的根 repo 條目。 |
| 根目錄不是 git repo | 建議建一個（`git init` + 一個私有 remote），這是概念最少的路徑。 |
| 想放進某個既有的 docs repo 再 symlink 回根目錄 | **不建議。** Windows 的 symlink 需要開發者模式或管理員權限，直接違反「跨平台一級公民」。 |

**必須處理的三個陷阱**

1. **`.gitignore` 白名單**：採用「`*` 加白名單」型 `.gitignore` 的 workspace（`example-workspace` 正是如此）會靜默地忽略 `gits.yaml`——`git add` 不報錯，檔案也不會進版控，直到在另一台機器發現什麼都沒同步過去。`gits init` 必須主動偵測（`git check-ignore -q gits.yaml`）並提示補上白名單行。

2. **雞生蛋**：manifest 進了根 repo，根 repo 就成了必須「先」拉的東西。`gits up` / `gits sync` 一律**先同步根 repo → 重新載入 manifest → 再處理其餘 repo**（§7.1、§7.3）。根 repo 無法 ff 時必須明確警告「repo list may be stale」，不可靜默使用舊清單。

3. **可預期的 merge 衝突**：兩台機器各自新增 repo，若條目都附加在 `repos` 尾端，就是同一位置的兩筆新增，**必然衝突**，而且是最不想手動解的那種 YAML 衝突。因此條目**依 `name` 排序插入**（§5.2）——兩邊的新增多半落在不同位置，git 自動 merge 就過了。成本近乎零。

**不該做的事**

- 不要把清單放在 home 目錄或全域註冊表。清單一旦離開 workspace 目錄，就無法跟著版控走，那正是痛點 3 的成因。全域註冊表（v2）只該存「workspace 在哪」，不該存「workspace 有什麼」。
- 不要讓 agent 直接編輯 `gits.yaml`。用 `gits add`（§7.9）。

### 10.2 為什麼 v1 不做 MCP server

要讓「任何 AI agent」都能用，**CLI + 穩定 JSON 是覆蓋面最廣的介面**：任何能執行 shell 的 agent 都能用，零設定、零協定綁定。MCP 只有支援 MCP 的 agent 能用，而且要求使用者額外設定 server。

順序也很重要：先把 CLI 的 `--json` 做紮實，日後 `gits mcp` 可以是一層極薄的包裝，把既有指令映射成 MCP tool——因為屆時所有邏輯早已是「結構化輸入 → 結構化輸出」。反過來若先做 MCP，邏輯會被寫進 server 裡，CLI 反而變成二等公民。

**低成本的補充**：讓 `gits` 能把自己的能力描述輸出給 agent 看——`gits help --json`（列出所有指令、旗標、退出碼、錯誤代號），或在 repo 裡放一份簡短的 `AGENTS.md`。Agent 對「有結構的能力描述」的利用率遠高於對 `--help` 散文的利用率。

### 10.3 建議搭配的 git 設定

不屬於 `gits` 的功能，但直接提升多 repo 工作流體驗：

- `push.autoSetupRemote=true`、`fetch.prune=true`、`pull.rebase=true`。
- 以 `includeIf "gitdir:<workspace>/"` 為整個 workspace 套用共用的 git 設定。
- 各 repo 執行 `git maintenance start` — 背景預抓能讓離線的 ahead/behind 更接近真實（`gits` v1 不主動讀 prefetch ref，僅照實標註資料可能過期）。

---

## 11. Roadmap（v2 以後）

以下為已識別但**不列入 v1** 的構想，僅記錄方向，細節待 v1 落地後再定。

**近期（v1.1）**

- **workspace 鎖**：`.gits/lock`。寫入類指令取排他鎖，唯讀指令不取。取不到鎖時**不等待**，直接以 `E_LOCKED` 失敗並回報持有者的 PID 與指令——讓呼叫者自己決定重試或放棄，靜默阻塞是最糟的選項。人與 agent 同時操作同一個 workspace 時需要。
- **plan / apply 分離**：`gits push --dry-run --json` 輸出計畫與 `planId`（計畫內容的雜湊）；`gits push --plan <planId>` 重新計算計畫，雜湊不符就拒絕執行。這讓「人批准的計畫」與「實際執行的計畫」保證一致——互動式 `[y/N]` 在 agent 手上給不了這個保證。
- **`gits list --format=markdown` 的原地更新**：直接維護 `CLAUDE.md` 中 `<!-- gits:begin -->` / `<!-- gits:end -->` 區塊，讓 agent 文件與實況永不漂移。
- **離線精度優化**：讀取 `refs/prefetch/<remote>/<branch>`（`git maintenance` 背景預抓所寫入）以提升離線 behind 判斷的準確度。v1 刻意不做——那是第二條 code path，且前提是使用者已啟用 `git maintenance`。

**依賴機制的深化**

- **宣告式依賴檔**：在 manifest 中表達 submodule 以外的相依（例如某服務的 compose 依賴另一個 repo 產出的 image、或工具對 sibling 目錄佈局的假設）。
- **影響分析**：不只回報「落後 3 個 commit」，而是分析那 3 個 commit 改了哪些檔案、與依賴方實際使用的部分是否重疊，把「可能被破壞」從猜測變成證據。這對 agent 的價值尤其高。
- **`gits freeze` / `gits restore`**：把當下所有 repo 的 SHA 寫入 `gits.lock` 並納入版控，取得 superproject 等級的可重現性，而不必承擔 submodule 的 UX 成本。

**操作面**

- `sync` 的 `--dirty=attempt` 模式（`rebase --autostash`），給願意承擔風險的使用者。
- 全螢幕 TUI：可上下選 repo、即時預覽 diff、勾選要提交的項目。
- 全域 workspace 註冊表（只記錄 workspace 位置，不記錄內容）。

**生態整合**

- `gits mcp`：MCP server 模式，作為 CLI 之上的薄包裝（§10.2）。
- `gits help --json`：機器可讀的能力描述。
- 與 tsrc／vcstool／Google repo 的 manifest 互轉。
- GitHub／GitLab API 整合：在 `status` 中顯示 PR／MR 狀態與 CI 結果。
- shell 補全（bash／zsh／PowerShell）。
- 泛用化的說明文件與英文 README（v1 文件以中文為主，公開發佈時需補）。
