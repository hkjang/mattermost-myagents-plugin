import manifest from 'manifest';
import React from 'react';
import type {Store} from 'redux';

import type {WebSocketMessage} from '@mattermost/client';
import type {Post} from '@mattermost/types/posts';
import type {GlobalState} from '@mattermost/types/store';

import {setSiteURL} from './client';
import ConfigSetting from './components/config_setting';
import PluginErrorBoundary from './components/error_boundary';
import MyAgentsBotPost from './components/myagents_bot_post';
import PostEventListener from './post_event_listener';
import {buildPluginWebSocketEventName, handleStreamingPostUpdateEvent} from './streaming';
import type {PluginRegistry} from './types/mattermost-webapp';

const SafeConfigSetting = (props: React.ComponentProps<typeof ConfigSetting>) => (
    <PluginErrorBoundary area={'myagents admin setting'}>
        <ConfigSetting {...props}/>
    </PluginErrorBoundary>
);

export default class Plugin {
    private readonly postEventListener = new PostEventListener();
    private botUserID = '';

    public async initialize(registry: PluginRegistry, store: Store<GlobalState>) {
        let siteURL = store.getState().entities.general.config.SiteURL;
        if (!siteURL) {
            siteURL = window.location.origin;
        }
        setSiteURL(siteURL);

        if (registry.registerAdminConsoleCustomSetting) {
            registry.registerAdminConsoleCustomSetting('Config', SafeConfigSetting);
        }

        this.refreshBotUserID();

        registry.registerWebSocketEventHandler(
            buildPluginWebSocketEventName(manifest.id, 'postupdate'),
            (msg) => {
                handleStreamingPostUpdateEvent(store, msg);
                this.postEventListener.handlePostUpdateWebsockets(msg as any);
            },
        );

        registry.registerWebSocketEventHandler('posted', (msg: WebSocketMessage<{post?: string | Post}>) => {
            this.handlePostedWebSocket(store, msg);
        });

        if (registry.registerPostTypeComponent) {
            registry.registerPostTypeComponent('custom_myagents_bot', (props: any) => (
                <PluginErrorBoundary area={'myagents bot post'}>
                    <MyAgentsBotPost
                        {...props}
                        websocketRegister={this.postEventListener.registerPostUpdateListener}
                        websocketUnregister={this.postEventListener.unregisterPostUpdateListener}
                    />
                </PluginErrorBoundary>
            ));
        }
    }

    private async refreshBotUserID() {
        try {
            const {getStatus} = await import('./client');
            const status = await getStatus();
            this.botUserID = status.bot?.user_id || '';
        } catch {
            this.botUserID = '';
        }
    }

    private handlePostedWebSocket(store: Store<GlobalState>, msg: WebSocketMessage<{post?: string | Post}>) {
        const post = parsePostedPost(msg?.data?.post);
        if (!post || !isMyAgentsDMPost(store.getState(), post, this.botUserID)) {
            return;
        }
        const rootPostID = post.root_id || post.id;
        store.dispatch({
            type: 'SELECT_POST',
            postId: rootPostID,
            channelId: post.channel_id,
            timestamp: Date.now(),
        } as any);
        focusRHSReplyBox();
    }
}

declare global {
    interface Window {
        registerPlugin(pluginId: string, plugin: Plugin): void;
    }
}

window.registerPlugin(manifest.id, new Plugin());

function parsePostedPost(raw?: string | Post): Post | null {
    if (!raw) {
        return null;
    }
    if (typeof raw !== 'string') {
        return raw;
    }
    try {
        return JSON.parse(raw) as Post;
    } catch {
        return null;
    }
}

function isMyAgentsDMPost(state: GlobalState, post: Post, botUserID: string) {
    if (!post.channel_id || !post.id) {
        return false;
    }
    const channel = state.entities.channels.channels[post.channel_id];
    if (!channel || channel.type !== 'D') {
        return false;
    }
    if (post.props?.myagents_bot !== 'true' && post.user_id !== botUserID) {
        return false;
    }
    const currentUserID = state.entities.users.currentUserId;
    return Boolean(currentUserID && post.user_id !== currentUserID);
}

function focusRHSReplyBox() {
    window.setTimeout(() => {
        const selectors = [
            '#reply_textbox',
            '[data-testid="reply_textbox"]',
            '.post-create__textarea textarea',
            '.post-create__textarea [contenteditable="true"]',
            '[aria-label="Reply"] textarea',
        ];
        for (const selector of selectors) {
            const element = document.querySelector(selector) as HTMLElement | null;
            if (element) {
                element.focus();
                break;
            }
        }
    }, 180);
}
