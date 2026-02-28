# Genesis 系統功能流程圖總覽

本文件整理了 Genesis 專案中各核心功能的詳細流程圖，涵蓋從系統啟動、訊息路由到 AI 代理執行迴圈的全過程。

---

## 1. 系統啟動流程 (System Startup Flow)

此流程描述了 `main.go` 如何協調各個模塊進行初始化。

```mermaid
sequenceDiagram
    participant Main as main.go
    participant Config as pkg/config
    participant Monitor as pkg/monitor
    participant LLM as pkg/llm
    participant Gateway as pkg/gateway
    participant Handler as pkg/handler

    Main->>Config: Load() (載入 config.json & system.json)
    Config-->>Main: 回傳 Config & SystemConfig

    Main->>Monitor: SetupEnvironment(LogLevel)
    Monitor-->>Main: 初始化 Logger & CLIMonitor

    Main->>LLM: NewFromConfig(LLM_Cfg, Sys_Cfg)
    LLM-->>Main: 回傳 LLMClient (含 Fallback 機制)

    Main->>LLM: NewChatHistory()
    LLM-->>Main: 回傳 ChatHistory 實例

    Main->>Gateway: NewGatewayBuilder()
    Main->>Gateway: WithSystemConfig / WithMonitor

    Main->>Gateway: WithChannelLoader (閉包: channels.LoadFromConfig)
    Main->>Gateway: WithHandlerFactory (閉包: handler.NewMessageHandler)

    Main->>Gateway: Build()
    Gateway->>Gateway: 載入並註冊 Channels (TG, Web)
    Gateway->>Handler: 透過 Factory 建立 ChatHandler
    Gateway-->>Main: 回傳 GatewayManager (gw)

    Main->>Gateway: gw.StartAll()
    Gateway->>Gateway: 啟動所有頻道監聽

    Main->>Main: 等待 SIGINT/SIGTERM 信號
    Main->>Gateway: gw.StopAll() (優雅關閉)
```

---
## 2. 訊息處理管道 (Message Handling Pipeline)

展示使用者訊息從接收到被處理的完整路徑。

```mermaid
flowchart LR
    User["👤 使用者"]
    subgraph Channels ["pkg/channels"]
        TG["Telegram"]
        Web["Web UI"]
    end
    subgraph Gateway ["pkg/gateway"]
        GM["GatewayManager.OnMessage"]
    end
    subgraph Logic ["pkg/handler"]
        CH["ChatHandler.OnMessage"]
        SC["Slash Command 處理"]
        Loop["Agentic Loop (LLM)"]
    end
    Monitor["pkg/monitor"]

    User -- 傳送訊息 --> TG & Web
    TG & Web -- 封裝 UnifiedMessage --> GM
    GM -- 記錄日誌 --> Monitor
    GM -- 轉發 --> CH
    CH -- 判斷 --> SC
    CH -- 加入歷史 --> Loop
    Loop -- 生成回覆 --> GM
    GM -- StreamReply --> TG & Web
    TG & Web -- 推播回覆 --> User
```

---
## 3. 代理執行迴圈 (Agentic Loop & Tool Execution)

核心業務邏輯 `processLLMStream` 的遞迴執行與工具呼叫流程。

```mermaid
flowchart TD
    Start["開始 processLLMStream"] --> Init["設定 LLM 逾時與工具格式"]
    Init --> LLM["調用 LLM.StreamChat"]
    LLM --> Collect["collectChunks (消費串流)"]

    Collect --> CheckTools{"偵測到 ToolCalls?"}

    CheckTools -- 是 --> StoreAsst["將 Assistant ToolCall 加入歷史"]
    StoreAsst --> ExecTools["遍歷執行 resolveAndCommitToolCall"]
    ExecTools --> ToolExec["工具執行 (OS 命令/截圖等)"]
    ToolExec --> StoreTool["將 Tool Result 加入歷史"]
    StoreTool --> SignalSystem["發送 role:system 信號至前端"]
    SignalSystem --> StreamTool["串流工具結果至前端展示"]
    StreamTool --> Recurse["遞迴調用 processLLMStream"]
    Recurse --> End

    CheckTools -- 否 --> NormalEnd{"正常結束? (StopReason)"}
    NormalEnd -- 是 --> Save["將最終 Assistant 訊息存入歷史"]
    Save --> End["結束流程"]

    NormalEnd -- 截斷 (Length) --> Warn["通知使用者內容被截斷"]
    Warn --> End

    NormalEnd -- 異常/錯誤 --> Retry{"可以重試? (RetryCount < Max)"}
    Retry -- 是 --> Wait["等待 RetryDelay"]
    Wait --> Recurse
    Retry -- 否 --> Fatal["回報錯誤訊息"]
    Fatal --> End
```

---
## 4. 串流與即時回饋 (Streaming & Real-time Feedback)

詳細描述串流塊 (Chunk) 如何被分類並即時推送到使用者介面。

```mermaid
sequenceDiagram
    participant LLM as LLM Client
    participant CH as ChatHandler (collectChunks)
    participant GW as GatewayManager
    participant UI as Channel (TG/Web)

    Note over CH: 初始化 Thinking Timer (500ms)

    par 串流監聽
        LLM->>CH: 發送 StreamChunk (含 Text/Thinking)
        Note over CH: 收到首個 Chunk, 停止 Thinking Timer
        CH->>CH: processChunk 分類處理
        alt 是 Thinking 塊 (且 ShowThinking=true)
            CH->>GW: 推送 BlockTypeThinking
            GW->>UI: 轉發至前端顯示 "AI 思考中..."
        else 是 Text 塊
            CH->>GW: 推送 BlockTypeText
            GW->>UI: 轉發並累加顯示正文
        else 是 Error 塊
            CH->>GW: 推送 BlockTypeError
            GW->>UI: 顯示錯誤警示
        end
    and 計時器監控
        Note over CH: 若 500ms 內無 Chunk 回傳
        CH->>GW: 觸發 SendSignal("thinking")
        GW->>UI: UI 顯示轉圈動畫
    end

    LLM->>CH: IsFinal = true
    CH->>GW: 關閉 blockCh
    GW->>UI: 結束當前訊息串流
```
