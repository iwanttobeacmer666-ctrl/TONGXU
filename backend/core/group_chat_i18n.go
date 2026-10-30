package core

const (
	MsgGroupChatCommands     MsgKey = "group_chat_commands"
	MsgGroupModelPicker      MsgKey = "group_model_picker"
	MsgGroupThinkingPicker   MsgKey = "group_thinking_picker"
	MsgGroupSettingSaved     MsgKey = "group_setting_saved"
	MsgGroupPickFirst        MsgKey = "group_pick_first"
	MsgGroupNativeDefault    MsgKey = "group_native_default"
	MsgGroupNoNativeData     MsgKey = "group_no_native_data"
	MsgGroupCodexBusy        MsgKey = "group_codex_busy"
	MsgGroupQueryTimeout     MsgKey = "group_query_timeout"
	MsgGroupContextInfo      MsgKey = "group_context_info"
	MsgGroupNoConversation   MsgKey = "group_no_conversation"
	MsgGroupCompacted        MsgKey = "group_compacted"
	MsgGroupNewConversation  MsgKey = "group_new_conversation"
	MsgGroupUnknownCommand   MsgKey = "group_unknown_command"
	MsgGroupConnectionFailed MsgKey = "group_connection_failed"
	MsgGroupLoginFailed      MsgKey = "group_login_failed"
	MsgGroupQuotaExceeded    MsgKey = "group_quota_exceeded"
	MsgGroupNativeFailed     MsgKey = "group_native_failed"
	MsgGroupQuotaHeader      MsgKey = "group_quota_header"
	MsgGroupQuotaLine        MsgKey = "group_quota_line"
)

var groupChatMessages = map[MsgKey]map[Language]string{
	MsgGroupCodexBusy:    {LangChinese: "Codex 当前繁忙，暂时无法回复，请稍后再试。", LangEnglish: "Codex is busy and cannot respond right now. Please try later.", LangTraditionalChinese: "Codex 目前忙碌，暫時無法回覆，請稍後再試。", LangJapanese: "Codex は現在混雑しています。後で再試行してください。", LangSpanish: "Codex está ocupado. Inténtalo más tarde."},
	MsgGroupQueryTimeout: {LangChinese: "Codex 查询超时，请稍后再试。", LangEnglish: "The Codex query timed out. Try again later.", LangTraditionalChinese: "Codex 查詢逾時，請稍後再試。", LangJapanese: "Codex の問い合わせがタイムアウトしました。", LangSpanish: "La consulta de Codex agotó el tiempo. Reintenta luego."},
	MsgGroupChatCommands: {
		LangChinese:            "/help 全部指令\n/model 模型列表；/model 编号 切换（仅管理员，全群共用）\n/thinking 思考强度；/thinking 编号 切换（仅管理员，兼容 /thinkng）\n/context 自己会话的最近上下文报告\n/compact 排队压缩自己的会话（可能消耗额度）\n/token 共享账号剩余额度与重置时间（全员可查）\n/new 重开自己的会话；保留文件、群设置和群公开背景",
		LangEnglish:            "/help all commands\n/model list; /model number changes the group model (owner only)\n/thinking efforts; /thinking number changes effort (owner only; /thinkng alias)\n/context your latest context report\n/compact queue native compaction of your conversation (may use quota)\n/token shared account quota and resets (everyone)\n/new restart your conversation, retaining files, group settings and public background",
		LangTraditionalChinese: "/help 全部指令\n/model 模型；/model 編號 切換（僅管理員，全群共用）\n/thinking 思考強度；/thinking 編號 切換（僅管理員，別名 /thinkng）\n/context 自己會話的最近用量\n/compact 排隊壓縮自己的會話（可能消耗額度）\n/token 共用帳號額度及重置時間（全員）\n/new 重開自己的會話，保留檔案、群設定和公開背景",
		LangJapanese:           "/help 全コマンド\n/model モデル一覧・番号で変更（所有者のみ、グループ共通）\n/thinking 推論強度・番号で変更（所有者のみ、別名 /thinkng）\n/context 自分の最新コンテキスト情報\n/compact 自分の会話を順番に圧縮（利用枠を消費する場合あり）\n/token 共通アカウントの利用枠（全員）\n/new 自分の会話を再開。ファイル・設定・公開背景は保持",
		LangSpanish:            "/help todos los comandos\n/model lista y selección por número (solo propietario; común al grupo)\n/thinking intensidades y selección (solo propietario; alias /thinkng)\n/context último contexto propio\n/compact comprimir la conversación propia en cola (puede consumir cuota)\n/token cuota compartida (todos)\n/new nueva conversación propia, conservando archivos, ajustes y contexto público",
	},
	MsgGroupModelPicker:      {LangChinese: "全群当前模型：%s\n思考强度：%s\n服务器模型目录：\n%s\n管理员回复 /model 编号，10分钟内有效；实际可用性以运行结果为准。", LangEnglish: "Group model: %s\nEffort: %s\nServer catalog:\n%s\nOwner: /model number within 10 minutes. Runtime access may vary.", LangTraditionalChinese: "全群模型：%s\n思考強度：%s\n伺服器目錄：\n%s\n管理員於10分鐘內回覆 /model 編號；實際可用性以執行結果為準。", LangJapanese: "共通モデル: %s\n推論強度: %s\n一覧:\n%s\n所有者は10分以内に /model 番号。実際のアクセスは実行時に判定します。", LangSpanish: "Modelo común: %s\nIntensidad: %s\nCatálogo:\n%s\nPropietario: /model número en 10 minutos; acceso sujeto a ejecución."},
	MsgGroupThinkingPicker:   {LangChinese: "当前模型：%s\n思考强度：%s\n该模型支持：\n%s\n管理员回复 /thinking 编号，10分钟内有效。", LangEnglish: "Model: %s\nEffort: %s\nSupported:\n%s\nOwner: /thinking number within 10 minutes.", LangTraditionalChinese: "模型：%s\n思考強度：%s\n支援：\n%s\n管理員於10分鐘內回覆 /thinking 編號。", LangJapanese: "モデル: %s\n推論強度: %s\n選択肢:\n%s\n所有者は10分以内に /thinking 番号。", LangSpanish: "Modelo: %s\nIntensidad: %s\nOpciones:\n%s\nPropietario: /thinking número en 10 minutos."},
	MsgGroupSettingSaved:     {LangChinese: "已保存全群设置：模型 %s，思考强度 %s。从下一项开始执行的任务生效，当前任务继续。", LangEnglish: "Group settings saved: model %s, effort %s. Applies to the next started task; current work continues.", LangTraditionalChinese: "已儲存群設定：模型 %s，強度 %s。從下一項開始執行的任務生效，目前任務繼續。", LangJapanese: "共通設定を保存: モデル %s、推論強度 %s。次のタスクから適用し、実行中の作業は継続します。", LangSpanish: "Ajustes guardados: modelo %s, intensidad %s. Se aplican a la próxima tarea; la actual continúa."},
	MsgGroupPickFirst:        {LangChinese: "选择列表已失效或模型已变化，请先发送 %s 查看最新列表，再按编号选择。", LangEnglish: "Picker expired or model changed. Send %s again before choosing a number.", LangTraditionalChinese: "清單已過期或模型已變更，請先傳送 %s 再選編號。", LangJapanese: "一覧が期限切れかモデルが変更されました。%s で再表示してください。", LangSpanish: "Lista caducada o modelo cambiado. Envía %s antes de elegir."},
	MsgGroupNativeDefault:    {LangChinese: "跟随 Codex 默认", LangEnglish: "Codex default", LangTraditionalChinese: "使用 Codex 預設", LangJapanese: "Codex の既定値", LangSpanish: "Predeterminado de Codex"},
	MsgGroupNoNativeData:     {LangChinese: "Codex 暂未返回可查询的数据，请稍后再试。上下文需要当前模型完成一次任务后才有报告。", LangEnglish: "Codex has no queryable data yet. Context reports require a task on the current model.", LangTraditionalChinese: "Codex 暫無可查詢資料；上下文需目前模型完成任務後才有報告。", LangJapanese: "現在参照できる情報がありません。コンテキスト情報には現在のモデルでの実行が必要です。", LangSpanish: "Codex todavía no ofrece datos consultables. El contexto requiere una tarea con el modelo actual."},
	MsgGroupContextInfo:      {LangChinese: "你自己的会话｜模型：%s\n上下文已用：%d / %d tokens\n剩余约：%.1f%%\n最近一次原生报告：%s", LangEnglish: "Your conversation | Model: %s\nContext: %d / %d tokens\nRemaining: %.1f%%\nLatest native report: %s", LangTraditionalChinese: "自己的會話｜模型：%s\n已用：%d / %d tokens\n剩餘約：%.1f%%\n最近原生報告：%s", LangJapanese: "自分の会話｜モデル: %s\n使用量: %d / %d tokens\n残り: %.1f%%\n最新レポート: %s", LangSpanish: "Tu conversación | Modelo: %s\nContexto: %d / %d tokens\nRestante: %.1f%%\nÚltimo informe: %s"},
	MsgGroupNoConversation:   {LangChinese: "你在本群还没有可续接的 Codex 会话。先 @我完成一次对话或办公任务。", LangEnglish: "You have no Codex conversation in this group yet. Mention me to start one.", LangTraditionalChinese: "你在本群尚無 Codex 會話，先 @我開始對話或任務。", LangJapanese: "このグループにはまだ自分の会話がありません。@して開始してください。", LangSpanish: "Todavía no tienes una conversación aquí. Menciona al bot para iniciar."},
	MsgGroupCompacted:        {LangChinese: "自己的 Codex 会话已完成原生压缩。", LangEnglish: "Your Codex conversation was compacted successfully.", LangTraditionalChinese: "自己的 Codex 會話已完成原生壓縮。", LangJapanese: "自分の Codex 会話を圧縮しました。", LangSpanish: "Tu conversación de Codex se ha comprimido."},
	MsgGroupNewConversation:  {LangChinese: "已重开你自己的 Codex 会话。工作文件、群模型设置和群公开聊天背景保留。", LangEnglish: "Your next task starts a new conversation. Files, group settings and public background are retained.", LangTraditionalChinese: "已重開自己的會話，保留檔案、群設定和公開背景。", LangJapanese: "次のタスクは新しい自分の会話で開始します。ファイル・設定・公開背景は保持します。", LangSpanish: "Tu próxima tarea inicia una conversación nueva. Se conservan archivos, ajustes y contexto público."},
	MsgGroupUnknownCommand:   {LangChinese: "未知指令，请发送 /help 查看用法。", LangEnglish: "Unknown command. Send /help.", LangTraditionalChinese: "未知指令，請傳送 /help。", LangJapanese: "不明なコマンドです。/help を確認してください。", LangSpanish: "Comando desconocido. Envía /help."},
	MsgGroupConnectionFailed: {LangChinese: "Codex 暂时连接不上，请稍后再试。若任务已开始或状态不明，未自动重试。", LangEnglish: "Codex is temporarily unreachable. Try later. Started or uncertain work was not replayed.", LangTraditionalChinese: "Codex 暫時無法連線，請稍後再試。已開始或狀態不明的任務未自動重試。", LangJapanese: "Codex に接続できません。後で再試行してください。開始済み・状態不明の作業は再実行していません。", LangSpanish: "Codex no está accesible. Reintenta luego; no se repitieron tareas iniciadas o inciertas."},
	MsgGroupLoginFailed:      {LangChinese: "Codex 登录状态不可用，请联系管理员检查专用账号登录。", LangEnglish: "Codex login is unavailable. Ask the owner to check the dedicated login.", LangTraditionalChinese: "Codex 登入不可用，請聯絡管理員。", LangJapanese: "Codex のログインを所有者に確認してもらってください。", LangSpanish: "Inicio de sesión no disponible; contacta al propietario."},
	MsgGroupQuotaExceeded:    {LangChinese: "Codex 本次请求的额度已达到限制，请用 /token 查看额度与重置时间。", LangEnglish: "Codex quota limit reached. Use /token for usage and reset information.", LangTraditionalChinese: "Codex 額度已達限制，請用 /token 查詢。", LangJapanese: "Codex の利用枠に達しました。/token で確認してください。", LangSpanish: "Límite de cuota alcanzado. Consulta /token."},
	MsgGroupNativeFailed:     {LangChinese: "Codex 暂时无法完成本次请求；没有自动重放任务，请稍后检查或重新发起。", LangEnglish: "Codex could not complete this request. It was not replayed automatically.", LangTraditionalChinese: "Codex 暫時無法完成此請求，未自動重新執行。", LangJapanese: "Codex は要求を完了できませんでした。自動再実行はしていません。", LangSpanish: "Codex no pudo completar la petición; no se repitió automáticamente."},
	MsgGroupQuotaHeader:      {LangChinese: "共享账号额度（全账号合计，不是剩余 token 数；? 表示未知）：", LangEnglish: "Shared account quota (account-wide, not remaining tokens; ? means unknown):", LangTraditionalChinese: "共用帳號額度（全帳號合計，非剩餘 token 數；? 為未知）：", LangJapanese: "共有アカウントの利用枠（残りトークン数ではありません。? は不明）:", LangSpanish: "Cuota de toda la cuenta compartida (no tokens restantes; ? indica desconocido):"},
	MsgGroupQuotaLine:        {LangChinese: "%s：剩余 %s；重置 %s", LangEnglish: "%s: remaining %s; resets %s", LangTraditionalChinese: "%s：剩餘 %s；重置 %s", LangJapanese: "%s: 残り %s、リセット %s", LangSpanish: "%s: restante %s; reinicio %s"},
}
