import React, {useEffect, useState} from 'react';

import type {ConnectionStatus, PluginStatus} from '../client';
import {getStatus, testConnection} from '../client';

const cardStyle: React.CSSProperties = {
    background: 'rgba(var(--center-channel-color-rgb), 0.04)',
    border: '1px solid rgba(var(--center-channel-color-rgb), 0.12)',
    borderRadius: '12px',
    display: 'flex',
    flexDirection: 'column',
    gap: '12px',
    padding: '16px',
};

export default function StatusPanel() {
    const [status, setStatus] = useState<PluginStatus | null>(null);
    const [connection, setConnection] = useState<ConnectionStatus | null>(null);
    const [message, setMessage] = useState('');
    const [loading, setLoading] = useState(true);
    const [testing, setTesting] = useState(false);

    useEffect(() => {
        let cancelled = false;
        async function load() {
            try {
                const pluginStatus = await getStatus();
                if (!cancelled) {
                    setStatus(pluginStatus);
                }
            } catch (error) {
                if (!cancelled) {
                    setMessage((error as Error).message);
                }
            } finally {
                if (!cancelled) {
                    setLoading(false);
                }
            }
        }
        load();
        return () => {
            cancelled = true;
        };
    }, []);

    async function onTestConnection() {
        setTesting(true);
        setMessage('');
        try {
            setConnection(await testConnection());
        } catch (error) {
            setMessage((error as Error).message);
        } finally {
            setTesting(false);
        }
    }

    return (
        <div style={cardStyle}>
            <strong>{'Langflow 상태'}</strong>
            {loading && <span>{'플러그인 상태를 불러오는 중입니다...'}</span>}
            {!loading && status && (
                <>
                    <div>{`기본 URL: ${status.base_url || '설정되지 않음'}`}</div>
                    <div>{`설정된 봇 수: ${status.bot_count}`}</div>
                    <div>{`허용 호스트: ${(status.allow_hosts || []).join(', ') || 'Langflow 호스트를 기본 사용'}`}</div>
                    <div>{`스트리밍 응답: ${status.streaming_enabled ? '사용' : '사용 안 함'}`}</div>
                    <div>{`스트리밍 갱신 주기: ${status.streaming_update_interval_ms || 0}ms`}</div>
                    {status.config_error && <div>{`설정 오류: ${status.config_error}`}</div>}
                    {status.bot_sync?.last_error && <div>{`봇 동기화 오류: ${status.bot_sync.last_error}`}</div>}
                    {(status.bots || []).length > 0 && (
                        <div style={{display: 'flex', flexDirection: 'column', gap: '10px'}}>
                            {(status.bots || []).map((bot) => {
                                const managed = (status.managed_bots || []).find((item) => item.bot_id === bot.id);
                                return (
                                    <div
                                        key={bot.id}
                                        style={{
                                            background: 'rgba(var(--center-channel-color-rgb), 0.03)',
                                            border: '1px solid rgba(var(--center-channel-color-rgb), 0.1)',
                                            borderRadius: '10px',
                                            display: 'flex',
                                            flexDirection: 'column',
                                            gap: '4px',
                                            padding: '12px',
                                        }}
                                    >
                                        <strong>{bot.display_name || bot.username}</strong>
                                        <span>{`@${bot.username} -> ${bot.flow_id}`}</span>
                                        {managed && <span>{`Mattermost 사용자: ${managed.user_id || '생성 대기 중'}`}</span>}
                                        {managed && <span>{`플러그인 관리: ${managed.registered ? '예' : '아니오'}, 활성 상태: ${managed.active ? '예' : '아니오'}`}</span>}
                                        {managed?.status_message && <span>{`상태: ${managed.status_message}`}</span>}
                                        {bot.description && <span>{bot.description}</span>}
                                    </div>
                                );
                            })}
                        </div>
                    )}
                    <button
                        className='btn btn-primary'
                        disabled={testing}
                        onClick={onTestConnection}
                        type='button'
                    >
                        {testing ? '연결 확인 중...' : '연결 테스트'}
                    </button>
                    {connection && (
                        <div>
                            <div>{connection.ok ? '연결에 성공했습니다.' : '연결에 실패했습니다.'}</div>
                            <div>{connection.url}</div>
                            <div style={{whiteSpace: 'pre-wrap'}}>{connection.message}</div>
                            {connection.error_code && <div>{`오류 코드: ${connection.error_code}`}</div>}
                            {connection.detail && <div style={{whiteSpace: 'pre-wrap'}}>{`상세: ${connection.detail}`}</div>}
                            {connection.hint && <div style={{whiteSpace: 'pre-wrap'}}>{`조치: ${connection.hint}`}</div>}
                            {connection.retryable !== undefined && <div>{`재시도 가능: ${connection.retryable ? '예' : '아니오'}`}</div>}
                        </div>
                    )}
                </>
            )}
            {message && <span>{message}</span>}
            <div style={{fontSize: '12px', opacity: 0.8}}>
                {'System Console에서 저장하면 이 플러그인이 관리하는 Mattermost 봇 계정이 생성되거나 갱신됩니다. 목록에서 제거한 봇은 비활성화됩니다.'}
            </div>
            <div style={{fontSize: '12px', opacity: 0.8}}>
                {'이후 사용자는 해당 봇과 DM을 하거나 채널에서 @멘션할 수 있고, 플러그인은 그 봇에 매핑된 Langflow run API를 호출합니다.'}
            </div>
            <div style={{fontSize: '12px', opacity: 0.8}}>
                {'스트리밍이 켜져 있으면 봇 답글을 먼저 하나 생성한 뒤, Langflow 토큰이 도착할 때마다 같은 포스트를 갱신합니다.'}
            </div>
        </div>
    );
}
