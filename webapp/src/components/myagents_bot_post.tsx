import React, {useEffect, useMemo, useRef, useState} from 'react';

import type {WebSocketMessage} from '@mattermost/client';

import PostText from './post_text';

type PostUpdateData = {
    post_id?: string;
    next?: string;
    control?: string;
};

type Props = {
    post: any;
    websocketRegister: (postID: string, listenerID: string, listener: (msg: WebSocketMessage<PostUpdateData>) => void) => void;
    websocketUnregister: (postID: string, listenerID: string) => void;
};

let stylesInjected = false;

export default function MyAgentsBotPost(props: Props) {
    const [message, setMessage] = useState(props.post.message || '');
    const [streaming, setStreaming] = useState(isStreaming(props.post));
    const listenerID = useRef(`myagents-${Math.random().toString(36).slice(2)}`);

    useEffect(() => {
        injectStyles();
    }, []);

    useEffect(() => {
        setMessage(props.post.message || '');
        setStreaming(isStreaming(props.post));
    }, [props.post.message, props.post.props?.myagents_streaming, props.post.props?.myagents_stream_status]);

    const listener = useMemo(() => {
        return (msg: WebSocketMessage<PostUpdateData>) => {
            const data = msg?.data || {};
            if (data.post_id !== props.post.id) {
                return;
            }
            if (typeof data.next === 'string') {
                setMessage(data.next);
            }
            if (data.control === 'start' || data.control === 'delta') {
                setStreaming(true);
            }
            if (data.control === 'end' || data.control === 'cancel') {
                setStreaming(false);
            }
        };
    }, [props.post.id]);

    useEffect(() => {
        props.websocketRegister(props.post.id, listenerID.current, listener);
        return () => props.websocketUnregister(props.post.id, listenerID.current);
    }, [listener, props.post.id, props.websocketRegister, props.websocketUnregister]);

    const parsed = splitThinking(message);

    return (
        <div className='myagents-post'>
            {parsed.thinking.map((item, index) => (
                <div className='myagents-thinking-panel' key={`${props.post.id}-thinking-${index}`}>
                    <div className='myagents-thinking-label'>{'Thinking'}</div>
                    <PostText
                        channelID={props.post.channel_id}
                        message={item.text}
                        postID={`${props.post.id}-thinking-${index}`}
                    />
                </div>
            ))}
            <PostText
                channelID={props.post.channel_id}
                message={parsed.body || (streaming ? '응답 생성 중...' : '')}
                postID={props.post.id}
                showCursor={streaming}
            />
        </div>
    );
}

function isStreaming(post: any) {
    return post?.props?.myagents_streaming === 'true' || post?.props?.myagents_stream_status === 'streaming';
}

function splitThinking(message: string) {
    const thinking: Array<{text: string}> = [];
    let body = message;
    const pattern = /<div class="myagents-thinking(?: myagents-thinking-complete)?">([\s\S]*?)<\/div>\s*/g;
    body = body.replace(pattern, (_full, content) => {
        thinking.push({
            text: String(content || '').trim(),
        });
        return '';
    }).trim();
    return {thinking, body};
}

function injectStyles() {
    if (stylesInjected || typeof document === 'undefined') {
        return;
    }
    const style = document.createElement('style');
    style.setAttribute('data-myagents-post', 'true');
    style.textContent = `
.myagents-post {
    display: flex;
    flex-direction: column;
    gap: 8px;
}
.myagents-thinking-panel {
    background: rgba(var(--center-channel-color-rgb), 0.04);
    border-left: 3px solid rgba(var(--center-channel-color-rgb), 0.18);
    border-radius: 6px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    padding: 8px 10px;
    overflow-x: auto;
}
.myagents-thinking-label {
    font-size: 11px;
    font-weight: 700;
    margin-bottom: 4px;
    text-transform: uppercase;
}
`;
    document.head.appendChild(style);
    stylesInjected = true;
}
