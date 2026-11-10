/// <reference types="vite/client" />

type TransferCounts = { channels: number; models: number; groups: number };
type TransferError = { code: string; message: string; stage: string; retryable: boolean; field?: string;
  operation_id?: string; replacement_preview?: TransferPreview };
type TransferResponse<T> = { ok: true; data: T } | { ok: false; error: TransferError };
type TransferNamedObject = { ref: string; original_name: string; name: string };
type TransferPreview = { preview_id: string; operation_id: string; file_sha256: string; plan_sha256: string;
  expires_at: string; file_info: { format_version: number; app_version: string }; counts: TransferCounts; rename_count: number;
  channels: (TransferNamedObject & { model_count: number; has_key: boolean; type: string })[];
  models: { ref: string; name: string; channel_ref: string }[];
  groups: (TransferNamedObject & { strategy: 'manual' | 'failover'; active_item_ref: string | null;
    items: { ref: string; model_ref: string; priority: string; position: number }[] })[] };
type TransferResult = { operation_id: string; state: 'committed'; committed_at: string; result_version: 1;
  counts: TransferCounts; rename_count: number; channels: (TransferNamedObject & { id: string })[];
  groups: (TransferNamedObject & { id: string })[]; replayed: boolean };
type TransferStatus = TransferResult | { operation_id: string; state: 'prepared'; counts: TransferCounts; expires_at?: string }
  | { operation_id: string; state: 'aborted'; counts: TransferCounts; failure_code: string; stage: string };
type TransferRecovery = { operation_id: string; state: 'prepared' | 'committed' | 'aborted'; created_at: string;
  confirmed_at: string; counts: TransferCounts; rename_count: number; failure_code?: string };
interface ConfigTransferApi {
  summary(): Promise<TransferResponse<{ counts: TransferCounts; can_export: boolean; ready: boolean; format_version: number;
    limits: { plaintext_bytes: number; file_bytes: number; channels: number; models: number; groups: number; items: number;
      preview_bytes: number; result_bytes: number; password_bytes: number; preview_ttl_seconds: number } }>>;
  exportConfig(input: { password: string }): Promise<TransferResponse<{ cancelled: true } | { file_name: string; bytes: number; counts: TransferCounts; format_version: number }>>;
  chooseImportFile(): Promise<TransferResponse<{ cancelled: true } | { file_token: string; file_name: string; bytes: number }>>;
  previewImport(input: { file_token: string; password: string }): Promise<TransferResponse<TransferPreview>>;
  commitImport(input: { preview_id: string; operation_id: string; file_sha256: string; plan_sha256: string }): Promise<TransferResponse<TransferStatus>>;
  operationResult(input: { operation_id: string }): Promise<TransferResponse<TransferStatus>>;
  recoverOperation(input: { operation_id: string }): Promise<TransferResponse<TransferStatus>>;
  recoveryList(input?: { cursor?: string; limit?: number }): Promise<TransferResponse<{ data: TransferRecovery[]; next_cursor: string | null }>>;
  acknowledge(input: { operation_id: string }): Promise<TransferResponse<unknown>>;
  discard(input: { preview_id?: string }): Promise<TransferResponse<unknown>>;
}

interface Window {
  // 既有 preload API 仍由 JS 消费；为新增的固定窄接口保留明确类型。
  api?: { configTransfer?: ConfigTransferApi; [key: string]: any };
}
