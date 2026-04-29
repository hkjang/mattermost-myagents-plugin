import manifest from 'manifest';
import React, {useEffect, useMemo, useRef, useState} from 'react';

import type {AdminPluginConfig, ConnectionStatus, PluginStatus} from '../client';
import {getAdminConfig, getStatus, testConnection} from '../client';

type CustomSettingProps = {
    id?: string;
    value?: unknown;
    disabled?: boolean;
    setByEnv?: boolean;
    helpText?: React.ReactNode;
    onChange: (id: string, value: unknown) => void;
    setSaveNeeded?: () => void;
};

const fieldStyle: React.CSSProperties = {
    border: '1px solid rgba(63, 67, 80, 0.18)',
    borderRadius: '6px',
    padding: '9px 10px',
    width: '100%',
};

const sectionStyle: React.CSSProperties = {
    background: 'white',
    border: '1px solid rgba(63, 67, 80, 0.12)',
    borderRadius: '8px',
    display: 'flex',
    flexDirection: 'column',
    gap: '16px',
    padding: '20px',
};

const gridStyle: React.CSSProperties = {
    display: 'grid',
    gap: '12px',
    gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
};

export default function ConfigSetting(props: CustomSettingProps) {
    const settingKey = props.id || 'Config';
    const disabled = Boolean(props.disabled || props.setByEnv);
    const [config, setConfig] = useState<AdminPluginConfig>(defaultConfig());
    const [status, setStatus] = useState<PluginStatus | null>(null);
    const [connection, setConnection] = useState<ConnectionStatus | null>(null);
    const [error, setError] = useState('');
    const [testing, setTesting] = useState(false);
    const lastSubmittedValueRef = useRef('');

    useEffect(() => {
        let cancelled = false;
        async function load() {
            const parsed = parseConfigValue(props.value);
            if (parsed.ok) {
                setConfig(parsed.config);
                return;
            }
            try {
                const response = await getAdminConfig();
                if (!cancelled) {
                    setConfig(normalizeConfig(response.config));
                    lastSubmittedValueRef.current = JSON.stringify(response.config, null, 2);
                }
            } catch (err) {
                if (!cancelled) {
                    setError((err as Error).message);
                }
            }
        }
        load();
        return () => {
            cancelled = true;
        };
    }, [props.value]);

    useEffect(() => {
        let cancelled = false;
        getStatus().then((nextStatus) => {
            if (!cancelled) {
                setStatus(nextStatus);
            }
        }).catch((err) => {
            if (!cancelled) {
                setError((err as Error).message);
            }
        });
        return () => {
            cancelled = true;
        };
    }, []);

    const validationMessages = useMemo(() => validate(config), [config]);

    const updateConfig = (patch: Partial<AdminPluginConfig>) => {
        const next = normalizeConfig({...config, ...patch});
        setConfig(next);
        const nextValue = JSON.stringify(next, null, 2);
        lastSubmittedValueRef.current = nextValue;
        props.onChange(settingKey, nextValue);
        props.setSaveNeeded?.();
    };

    const runTest = async () => {
        setTesting(true);
        setConnection(null);
        try {
            setConnection(await testConnection());
        } catch (err) {
            setError((err as Error).message);
        } finally {
            setTesting(false);
        }
    };

    return (
        <div style={{display: 'flex', flexDirection: 'column', gap: '16px'}}>
            <section style={sectionStyle}>
                <div style={{display: 'flex', justifyContent: 'space-between', gap: '12px'}}>
                    <div>
                        <strong>{'myagents 설정'}</strong>
                        <div style={{fontSize: '13px', opacity: 0.74}}>
                            {'@myagents 봇, 사용자별 opencode 도메인, JupyterHub 서버 제어를 설정합니다.'}
                        </div>
                    </div>
                    <span style={{fontSize: '12px', fontWeight: 700}}>{manifest.version}</span>
                </div>
                {props.helpText}
                {props.setByEnv && <Notice text={'이 설정은 환경 변수로 관리되어 여기에서 수정할 수 없습니다.'}/>}
                {error && <Notice text={error}/>}
                {validationMessages.map((message) => <Notice key={message} text={message}/>)}
                {status?.config_error && <Notice text={status.config_error}/>}
            </section>

            <section style={sectionStyle}>
                <strong>{'Mattermost 봇 및 opencode 매핑'}</strong>
                <div style={gridStyle}>
                    <Field label={'BotUsername'}>
                        <input disabled={disabled} onChange={(e) => updateConfig({bot_username: e.target.value})} style={fieldStyle} value={config.bot_username}/>
                    </Field>
                    <Field label={'BaseDomainSuffix'}>
                        <input disabled={disabled} onChange={(e) => updateConfig({base_domain_suffix: e.target.value})} style={fieldStyle} value={config.base_domain_suffix}/>
                    </Field>
                    <Field label={'UserIDMappingMode'}>
                        <select disabled={disabled} onChange={(e) => updateConfig({user_id_mapping_mode: e.target.value})} style={fieldStyle} value={config.user_id_mapping_mode}>
                            <option value='username'>{'username'}</option>
                        </select>
                    </Field>
                    <Field label={'RequestTimeoutSec'}>
                        <input disabled={disabled} min={1} onChange={(e) => updateConfig({request_timeout_sec: parseNumber(e.target.value, 60)})} style={fieldStyle} type='number' value={config.request_timeout_sec}/>
                    </Field>
                </div>
                <Checkbox disabled={disabled} label={'EnableAsync'} onChange={(value) => updateConfig({enable_async: value})} value={config.enable_async}/>
                <Field label={'AsyncThresholdSec'}>
                    <input disabled={disabled} min={1} onChange={(e) => updateConfig({async_threshold_sec: parseNumber(e.target.value, 30)})} style={fieldStyle} type='number' value={config.async_threshold_sec}/>
                </Field>
            </section>

            <section style={sectionStyle}>
                <strong>{'JupyterHub 서버 제어'}</strong>
                <Field label={'JupyterHubBaseURL'}>
                    <input disabled={disabled} onChange={(e) => updateConfig({jupyterhub_base_url: e.target.value})} style={fieldStyle} value={config.jupyterhub_base_url}/>
                </Field>
                <Field label={'JupyterHubAPIToken'}>
                    <input disabled={disabled} onChange={(e) => updateConfig({jupyterhub_api_token: e.target.value})} placeholder={'저장된 토큰은 다시 표시되지 않습니다.'} style={fieldStyle} type='password' value={config.jupyterhub_api_token}/>
                </Field>
                <div style={gridStyle}>
                    <Field label={'ServerStartTimeoutSec'}>
                        <input disabled={disabled} min={1} onChange={(e) => updateConfig({server_start_timeout_sec: parseNumber(e.target.value, 180)})} style={fieldStyle} type='number' value={config.server_start_timeout_sec}/>
                    </Field>
                    <Field label={'ServerStopTimeoutSec'}>
                        <input disabled={disabled} min={1} onChange={(e) => updateConfig({server_stop_timeout_sec: parseNumber(e.target.value, 120)})} style={fieldStyle} type='number' value={config.server_stop_timeout_sec}/>
                    </Field>
                    <Field label={'ServerStatusPollIntervalSec'}>
                        <input disabled={disabled} min={1} onChange={(e) => updateConfig({server_status_poll_interval_sec: parseNumber(e.target.value, 3)})} style={fieldStyle} type='number' value={config.server_status_poll_interval_sec}/>
                    </Field>
                </div>
                <Checkbox disabled={disabled} label={'AutoStartServer'} onChange={(value) => updateConfig({auto_start_server: value})} value={config.auto_start_server}/>
                <Checkbox disabled={disabled} label={'AllowUserStartServer'} onChange={(value) => updateConfig({allow_user_start_server: value})} value={config.allow_user_start_server}/>
                <Checkbox disabled={disabled} label={'AllowUserStopServer'} onChange={(value) => updateConfig({allow_user_stop_server: value})} value={config.allow_user_stop_server}/>
                <Checkbox disabled={disabled} label={'EnableDebugLogs'} onChange={(value) => updateConfig({enable_debug_logs: value})} value={config.enable_debug_logs}/>
            </section>

            <section style={sectionStyle}>
                <strong>{'상태'}</strong>
                <div>{`봇: @${status?.bot_username || config.bot_username} (${status?.bot?.active ? 'active' : 'not active'})`}</div>
                <div>{`opencode URL 예시: https://hkjang.${config.base_domain_suffix || 'kubagents.koreacb.com'}`}</div>
                <button className='btn btn-primary' disabled={testing} onClick={runTest} type='button'>
                    {testing ? '확인 중...' : 'JupyterHub 연결 확인'}
                </button>
                {connection && (
                    <Notice text={connection.ok ? `연결 성공 (${connection.status_code || 200})` : connection.message || '연결 실패'}/>
                )}
            </section>
        </div>
    );
}

function Field(props: {label: string; children: React.ReactNode}) {
    return (
        <label style={{display: 'flex', flexDirection: 'column', gap: '6px'}}>
            <span style={{fontWeight: 600}}>{props.label}</span>
            {props.children}
        </label>
    );
}

function Checkbox(props: {label: string; value: boolean; disabled: boolean; onChange: (value: boolean) => void}) {
    return (
        <label style={{alignItems: 'center', display: 'flex', gap: '8px'}}>
            <input checked={props.value} disabled={props.disabled} onChange={(e) => props.onChange(e.target.checked)} type='checkbox'/>
            <span>{props.label}</span>
        </label>
    );
}

function Notice(props: {text: string}) {
    return (
        <div style={{background: 'rgba(255, 188, 0, 0.12)', border: '1px solid rgba(255, 188, 0, 0.35)', borderRadius: '6px', padding: '10px 12px'}}>
            {props.text}
        </div>
    );
}

function defaultConfig(): AdminPluginConfig {
    return {
        bot_username: 'myagents',
        base_domain_suffix: 'kubagents.koreacb.com',
        user_id_mapping_mode: 'username',
        request_timeout_sec: 60,
        enable_async: true,
        async_threshold_sec: 30,
        jupyterhub_base_url: 'https://jupyterhub.kubagents-ofc.koreacb.com',
        jupyterhub_api_token: '',
        auto_start_server: false,
        server_start_timeout_sec: 180,
        server_stop_timeout_sec: 120,
        server_status_poll_interval_sec: 3,
        allow_user_stop_server: true,
        allow_user_start_server: true,
        enable_debug_logs: false,
    };
}

function parseConfigValue(value: unknown): {ok: true; config: AdminPluginConfig} | {ok: false; config: AdminPluginConfig} {
    if (!value) {
        return {ok: false, config: defaultConfig()};
    }
    try {
        const parsed = typeof value === 'string' ? JSON.parse(value) : value;
        return {ok: true, config: normalizeConfig(parsed as Partial<AdminPluginConfig>)};
    } catch {
        return {ok: false, config: defaultConfig()};
    }
}

function normalizeConfig(value: Partial<AdminPluginConfig>): AdminPluginConfig {
    const defaults = defaultConfig();
    return {
        ...defaults,
        ...value,
        bot_username: stringValue(value.bot_username || defaults.bot_username),
        base_domain_suffix: stringValue(value.base_domain_suffix || defaults.base_domain_suffix),
        user_id_mapping_mode: 'username',
        request_timeout_sec: parseNumber(value.request_timeout_sec, defaults.request_timeout_sec),
        async_threshold_sec: parseNumber(value.async_threshold_sec, defaults.async_threshold_sec),
        server_start_timeout_sec: parseNumber(value.server_start_timeout_sec, defaults.server_start_timeout_sec),
        server_stop_timeout_sec: parseNumber(value.server_stop_timeout_sec, defaults.server_stop_timeout_sec),
        server_status_poll_interval_sec: parseNumber(value.server_status_poll_interval_sec, defaults.server_status_poll_interval_sec),
        enable_async: value.enable_async ?? defaults.enable_async,
        auto_start_server: value.auto_start_server ?? defaults.auto_start_server,
        allow_user_stop_server: value.allow_user_stop_server ?? defaults.allow_user_stop_server,
        allow_user_start_server: value.allow_user_start_server ?? defaults.allow_user_start_server,
        enable_debug_logs: Boolean(value.enable_debug_logs),
    };
}

function validate(config: AdminPluginConfig) {
    const messages: string[] = [];
    if (!config.bot_username.trim()) {
        messages.push('BotUsername은 필수입니다.');
    }
    if (!config.base_domain_suffix.trim()) {
        messages.push('BaseDomainSuffix는 필수입니다.');
    }
    if (!config.jupyterhub_base_url.trim()) {
        messages.push('JupyterHubBaseURL은 필수입니다.');
    }
    return messages;
}

function parseNumber(value: unknown, fallback: number) {
    const parsed = Number(value);
    return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
}

function stringValue(value: unknown) {
    return typeof value === 'string' ? value : String(value || '');
}
