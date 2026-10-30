package core

const (
	MsgGroupMemberBusy     MsgKey = "group_member_busy"
	MsgGroupTasks          MsgKey = "group_tasks"
	MsgGroupTaskStopped    MsgKey = "group_task_stopped"
	MsgGroupTaskUncertain  MsgKey = "group_task_uncertain"
	MsgGroupTaskPublishing MsgKey = "group_task_publishing"
)

func init() {
	groupChatMessages[MsgGroupMemberBusy] = map[Language]string{LangChinese: "你已有一项排队或执行中的任务，请等它结束后再提交。帮助、文件查询、确认指令仍可使用。", LangEnglish: "You already have an unfinished task. Wait before submitting another. Help, file queries, and confirmations remain available.", LangTraditionalChinese: "你已有一項排隊或執行中的任務，請等它結束後再提交。說明、檔案查詢、確認指令仍可使用。", LangJapanese: "未完了のタスクがあります。終了後に再送してください。ヘルプ、ファイル照会、確認コマンドは利用できます。", LangSpanish: "Ya tienes una tarea pendiente. Espera a que termine antes de enviar otra. Ayuda, consultas de archivos, y confirmaciones siguen disponibles."}
	groupChatMessages[MsgGroupTasks] = map[Language]string{LangChinese: "当前群任务：编号 | 发起人 | queued/running/stopping/publishing | 入队时间 | 开始时间\n管理员：/stop 编号。/read member:用户ID/文件名；/delete member:用户ID/文件名。", LangEnglish: "Group tasks: ID | actor | state | queued | started. Owner: /stop ID. Files: /read or /delete member:USER/file.", LangTraditionalChinese: "目前群任務：編號 | 發起人 | 狀態 | 入隊 | 開始。管理員 /stop 編號；/read 或 /delete member:用戶ID/檔名。", LangJapanese: "グループタスク: ID | ユーザー | 状態 | 待機 | 開始。所有者: /stop ID。ファイル: /read または /delete member:USER/file。", LangSpanish: "Tareas del grupo: ID | usuario | estado | cola | inicio. Propietario: /stop ID. Archivos: /read o /delete member:USER/file."}
	groupChatMessages[MsgGroupTaskStopped] = map[Language]string{LangChinese: "任务已停止，清理已完成；未发布的产物不会发布，已保存文件保留。", LangEnglish: "Task stopped and cleanup completed. Unpublished outputs discarded; saved files retained.", LangTraditionalChinese: "任務已停止並完成清理；未發布產物不發布，已存檔案保留。", LangJapanese: "タスク停止とクリーンアップ完了。未公開出力は公開せず、保存済みファイルは保持します。", LangSpanish: "Tarea detenida y limpieza terminada. No se publican resultados pendientes; se conservan archivos guardados."}
	groupChatMessages[MsgGroupTaskUncertain] = map[Language]string{LangChinese: "已请求停止，但清理尚未确认或失败；不能视为已停止，请管理员检查本机。", LangEnglish: "Stop requested; cleanup pending or failed. Stop is not confirmed; check locally.", LangTraditionalChinese: "已請求停止，但清理未確認或失敗；請檢查本機。", LangJapanese: "停止要求済みですが、クリーンアップ未確認または失敗。ローカルで確認してください。", LangSpanish: "Parada solicitada, limpieza pendiente o fallida. Comprueba el equipo."}
	groupChatMessages[MsgGroupTaskPublishing] = map[Language]string{LangChinese: "任务已进入结果发布阶段，无法再接受停止；已保存文件不会自动回滚。", LangEnglish: "Task is publishing; stop cannot be accepted. Saved files are not rolled back.", LangTraditionalChinese: "任務正在發布，無法再停止；已存檔案不自動回滾。", LangJapanese: "結果公開中のため停止できません。保存済みファイルは戻しません。", LangSpanish: "La tarea está publicando; no admite parada ni revierte archivos guardados."}
}
