import React, {useEffect, useMemo, useState} from 'react';
import {useSelector} from 'react-redux';

import type {GlobalState} from '@mattermost/types/store';

import type {BotDefinition, BotInputField, BotRunResult, ExecutionRecord} from '../client';
import {getBots, getHistory, runBot} from '../client';

const containerStyle: React.CSSProperties = {
    display: 'flex',
    flexDirection: 'column',
    gap: '16px',
    padding: '16px',
};

const cardStyle: React.CSSProperties = {
    background: 'rgba(var(--center-channel-color-rgb), 0.04)',
    border: '1px solid rgba(var(--center-channel-color-rgb), 0.12)',
    borderRadius: '12px',
    padding: '12px',
};

const fieldStyle: React.CSSProperties = {
    border: '1px solid rgba(var(--center-channel-color-rgb), 0.16)',
    borderRadius: '8px',
    padding: '10px 12px',
    width: '100%',
};

export default function RHSPane() {
    const channelId = useSelector((state: GlobalState) => state.entities.channels.currentChannelId);
    const selectedPostId = useSelector((state: GlobalState) => (state as any).views?.rhs?.selectedPostId as string | undefined);

    const [bots, setBots] = useState<BotDefinition[]>([]);
    const [history, setHistory] = useState<ExecutionRecord[]>([]);
    const [selectedBotId, setSelectedBotId] = useState('');
    const [prompt, setPrompt] = useState('');
    const [includeContext, setIncludeContext] = useState(true);
    const [inputs, setInputs] = useState<Record<string, unknown>>({});
    const [loading, setLoading] = useState(true);
    const [submitting, setSubmitting] = useState(false);
    const [message, setMessage] = useState('');
    const [lastResult, setLastResult] = useState<BotRunResult | null>(null);

    const selectedBot = useMemo(
        () => bots.find((bot) => bot.id === selectedBotId) || bots[0],
        [bots, selectedBotId],
    );

    useEffect(() => {
        let cancelled = false;
        async function load() {
            setLoading(true);
            setMessage('');
            try {
                const [loadedBots, loadedHistory] = await Promise.all([
                    getBots(channelId),
                    getHistory(5),
                ]);
                if (cancelled) {
                    return;
                }
                setBots(loadedBots);
                setHistory(loadedHistory);
                if (loadedBots.length > 0) {
                    const initialBot = loadedBots[0];
                    setSelectedBotId(initialBot.id);
                    setIncludeContext(Boolean(initialBot.include_context_by_default));
                    setInputs(buildInitialInputs(initialBot.input_schema || []));
                } else {
                    setSelectedBotId('');
                    setInputs({});
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
    }, [channelId]);

    useEffect(() => {
        if (!selectedBot) {
            return;
        }
        setIncludeContext(Boolean(selectedBot.include_context_by_default));
        setInputs(buildInitialInputs(selectedBot.input_schema || []));
    }, [selectedBotId, selectedBot]);

    async function submit() {
        if (!selectedBot || !channelId) {
            return;
        }
        setSubmitting(true);
        setMessage('');
        try {
            const result = await runBot({
                bot_id: selectedBot.id,
                channel_id: channelId,
                root_id: selectedPostId,
                prompt,
                include_context: includeContext,
                inputs,
            });
            setLastResult(result);
            setPrompt('');
            setHistory(await getHistory(5));
            setMessage(`@${selectedBot.username} posted a bot reply in Mattermost and will stream updates there when enabled.`);
        } catch (error) {
            setMessage((error as Error).message);
        } finally {
            setSubmitting(false);
        }
    }

    return (
        <div style={containerStyle}>
            <section style={cardStyle}>
                <div style={{display: 'flex', flexDirection: 'column', gap: '8px'}}>
                    <strong>{'Ask a Langflow Bot'}</strong>
                    <span style={{fontSize: '12px', opacity: 0.8}}>
                        {'Each bot in this list is bound to a single Langflow flow. Pick a bot, send a prompt, and the selected bot will stream its reply into the channel or thread when streaming is enabled.'}
                    </span>
                    {loading && <span>{'Loading bots...'}</span>}
                    {!loading && bots.length === 0 && <span>{'No Langflow bots are available in this channel.'}</span>}
                    {!loading && bots.length > 0 && (
                        <>
                            <select
                                value={selectedBot?.id || ''}
                                onChange={(event) => setSelectedBotId(event.target.value)}
                                style={fieldStyle}
                            >
                                {bots.map((bot) => (
                                    <option
                                        key={bot.id}
                                        value={bot.id}
                                    >
                                        {`${bot.display_name || bot.username} (@${bot.username})`}
                                    </option>
                                ))}
                            </select>
                            <div style={{fontSize: '12px', opacity: 0.8}}>
                                {`Bound flow: ${selectedBot?.flow_id || 'Unknown'}`}
                            </div>
                            {selectedBot?.description && (
                                <span style={{opacity: 0.8}}>{selectedBot.description}</span>
                            )}
                            <textarea
                                value={prompt}
                                onChange={(event) => setPrompt(event.target.value)}
                                placeholder={selectedBot ? `Message @${selectedBot.username}...` : 'Ask Langflow something...'}
                                rows={6}
                                style={{...fieldStyle, resize: 'vertical'}}
                            />
                            {(selectedBot?.input_schema || []).map((field) => renderField(field, inputs, setInputs))}
                            <label style={{display: 'flex', gap: '8px', alignItems: 'center'}}>
                                <input
                                    type='checkbox'
                                    checked={includeContext}
                                    onChange={(event) => setIncludeContext(event.target.checked)}
                                />
                                {'Include recent thread or channel context'}
                            </label>
                            <button
                                className='btn btn-primary'
                                disabled={submitting || !prompt.trim()}
                                onClick={submit}
                                type='button'
                            >
                                {submitting ? 'Sending to bot...' : `Send as @${selectedBot?.username || 'bot'}`}
                            </button>
                        </>
                    )}
                    {message && <span>{message}</span>}
                </div>
            </section>

            {lastResult && (
                <section style={cardStyle}>
                    <strong>{'Latest Result'}</strong>
                    <div>{`${lastResult.bot_name || lastResult.bot_username} - ${lastResult.status}`}</div>
                    <div>{`Flow: ${lastResult.flow_id}`}</div>
                    {lastResult.error_message && <div style={{whiteSpace: 'pre-wrap'}}>{lastResult.error_message}</div>}
                    {lastResult.error_code && <div>{`Code: ${lastResult.error_code}`}</div>}
                    {lastResult.error_hint && <div>{`Hint: ${lastResult.error_hint}`}</div>}
                    {lastResult.request_url && <div style={{wordBreak: 'break-all'}}>{`URL: ${lastResult.request_url}`}</div>}
                    {lastResult.retryable !== undefined && <div>{`Retryable: ${lastResult.retryable ? 'Yes' : 'No'}`}</div>}
                    {lastResult.correlation_id && <div>{`Correlation: ${lastResult.correlation_id}`}</div>}
                </section>
            )}

            <section style={cardStyle}>
                <strong>{'Recent History'}</strong>
                <div style={{display: 'flex', flexDirection: 'column', gap: '8px', marginTop: '8px'}}>
                    {history.length === 0 && <span>{'No executions yet.'}</span>}
                    {history.map((item) => (
                        <div
                            key={item.correlation_id}
                            style={{fontSize: '12px'}}
                        >
                            <strong>{item.bot_name || item.bot_username}</strong>
                            <div>{`@${item.bot_username} -> ${item.flow_id}`}</div>
                            <div>{`${item.status} via ${item.source}`}</div>
                            {item.error_message && <div style={{whiteSpace: 'pre-wrap'}}>{item.error_message}</div>}
                            {item.error_code && <div>{`Code: ${item.error_code}`}</div>}
                        </div>
                    ))}
                </div>
            </section>
        </div>
    );
}

function buildInitialInputs(fields: BotInputField[]) {
    return fields.reduce<Record<string, unknown>>((acc, field) => {
        if (field.default_value !== undefined) {
            acc[field.name] = field.default_value;
        } else if (field.type === 'bool') {
            acc[field.name] = false;
        } else {
            acc[field.name] = '';
        }
        return acc;
    }, {});
}

function renderField(
    field: BotInputField,
    inputs: Record<string, unknown>,
    setInputs: React.Dispatch<React.SetStateAction<Record<string, unknown>>>,
) {
    const currentValue = inputs[field.name];
    const onChange = (value: unknown) => setInputs((current) => ({...current, [field.name]: value}));
    let control: React.ReactNode;

    if (field.type === 'bool') {
        control = (
            <label style={{display: 'flex', gap: '8px', alignItems: 'center'}}>
                <input
                    checked={Boolean(currentValue)}
                    onChange={(event) => onChange(event.target.checked)}
                    type='checkbox'
                />
                {field.placeholder || 'Enabled'}
            </label>
        );
    } else if (field.type === 'textarea') {
        control = (
            <textarea
                rows={4}
                style={{...fieldStyle, resize: 'vertical'}}
                value={String(currentValue ?? '')}
                onChange={(event) => onChange(event.target.value)}
                placeholder={field.placeholder}
            />
        );
    } else {
        control = (
            <input
                style={fieldStyle}
                type={field.type === 'number' ? 'number' : 'text'}
                value={String(currentValue ?? '')}
                onChange={(event) => onChange(field.type === 'number' ? Number(event.target.value) : event.target.value)}
                placeholder={field.placeholder}
            />
        );
    }

    return (
        <div
            key={field.name}
            style={{display: 'flex', flexDirection: 'column', gap: '6px'}}
        >
            <label style={{fontWeight: 600}}>{field.label || field.name}</label>
            {field.description && <span style={{fontSize: '12px', opacity: 0.8}}>{field.description}</span>}
            {control}
        </div>
    );
}
