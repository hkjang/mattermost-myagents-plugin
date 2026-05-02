import manifest from 'manifest';

let siteURL = '';

type RequestOptions = Omit<RequestInit, 'headers'> & {
    headers?: Record<string, string>;
};

export type AdminPluginConfig = {
    bot_username: string;
    base_domain_suffix: string;
    user_id_mapping_mode: string;
    request_timeout_sec: number;
    enable_async: boolean;
    async_threshold_sec: number;
    jupyterhub_base_url: string;
    jupyterhub_api_token: string;
    auto_start_server: boolean;
    server_start_timeout_sec: number;
    server_stop_timeout_sec: number;
    server_status_poll_interval_sec: number;
    allow_user_stop_server: boolean;
    allow_user_start_server: boolean;
    enable_debug_logs: boolean;
};

export type AdminConfigResponse = {
    config: AdminPluginConfig;
    source: string;
};

export type PluginStatus = {
    plugin_id: string;
    bot: {
        username: string;
        display_name: string;
        user_id: string;
        active: boolean;
        last_error?: string;
        updated_at: number;
    };
    bot_username: string;
    base_domain_suffix: string;
    jupyterhub_base_url: string;
    config_error?: string;
    base_url?: string;
    bot_count?: number;
    allow_hosts?: string[];
    streaming_enabled?: boolean;
    streaming_update_interval_ms?: number;
    bot_sync?: {
        last_error?: string;
    };
    bots?: BotDefinition[];
    managed_bots?: Array<{
        bot_id: string;
        user_id?: string;
        registered?: boolean;
        active?: boolean;
        status_message?: string;
    }>;
};

export type ConnectionStatus = {
    ok: boolean;
    status_code?: number;
    url?: string;
    message?: string;
    error_code?: string;
    detail?: string;
    hint?: string;
    retryable?: boolean;
};

export type BotInputField = {
    name: string;
    label?: string;
    description?: string;
    type?: string;
    placeholder?: string;
    default_value?: unknown;
};

export type BotDefinition = {
    id: string;
    username: string;
    display_name?: string;
    description?: string;
    flow_id?: string;
    include_context_by_default?: boolean;
    input_schema?: BotInputField[];
};

export type BotRunResult = {
    bot_name?: string;
    bot_username?: string;
    status: string;
    flow_id?: string;
    error_message?: string;
    error_code?: string;
    error_hint?: string;
    request_url?: string;
    retryable?: boolean;
    correlation_id?: string;
};

export type ExecutionRecord = BotRunResult & {
    source?: string;
};

export function setSiteURL(value: string) {
    siteURL = value.replace(/\/+$/, '');
}

function pluginURL(path: string) {
    const base = siteURL || window.location.origin;
    return `${base}/plugins/${manifest.id}/api/v1${path}`;
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
    const response = await fetch(pluginURL(path), {
        ...options,
        headers: {
            'Content-Type': 'application/json',
            ...(options.headers || {}),
        },
    });

    const data = await response.json().catch(() => ({}));
    if (!response.ok) {
        const failure = data as {error?: string; error_message?: string};
        throw new Error(failure.error || failure.error_message || 'Request failed');
    }
    return data as T;
}

export async function getStatus() {
    return request<PluginStatus>('/status');
}

export async function getAdminConfig() {
    return request<AdminConfigResponse>('/config');
}

export async function testConnection() {
    return request<ConnectionStatus>('/test', {method: 'POST'});
}

export async function getBots(channelID?: string) {
    const query = channelID ? `?channel_id=${encodeURIComponent(channelID)}` : '';
    return request<BotDefinition[]>(`/bots${query}`);
}

export async function getHistory(limit = 5) {
    return request<ExecutionRecord[]>(`/history?limit=${encodeURIComponent(String(limit))}`);
}

export async function runBot(payload: {
    bot_id: string;
    channel_id: string;
    root_id?: string;
    prompt: string;
    include_context: boolean;
    inputs: Record<string, unknown>;
}) {
    return request<BotRunResult>('/run', {
        method: 'POST',
        body: JSON.stringify(payload),
    });
}
