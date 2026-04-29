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
};

export type ConnectionStatus = {
    ok: boolean;
    status_code?: number;
    url?: string;
    message?: string;
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
