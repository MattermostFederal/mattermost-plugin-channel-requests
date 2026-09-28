import React from 'react';
import {useDispatch, useSelector} from 'react-redux';

import {
    closeHubModal,
    getCurrentChannelId,
    getCurrentTeamId,
    isCurrentUserTeamAdmin,
    isHubModalOpen,
    openAdminRequestModal,
    openBotRequestModal,
    openIncomingWebhookModal,
    openOutgoingWebhookModal,
    openRequestModal,
    openTeamAdminRequestModal,
    openTeamCreationModal,
} from './store';
import type {GlobalState} from './store';

const overlayStyle: React.CSSProperties = {
    position: 'fixed', inset: 0, background: 'rgba(0, 0, 0, 0.5)',
    display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1050,
};
const dialogStyle: React.CSSProperties = {
    background: 'var(--center-channel-bg, #fff)', color: 'var(--center-channel-color, #3d3c40)',
    borderRadius: 8, width: 600, maxWidth: '92vw', maxHeight: '90vh', overflowY: 'auto',
    padding: 24, boxShadow: '0 8px 24px rgba(0, 0, 0, 0.24)',
};
const gridStyle: React.CSSProperties = {
    display: 'grid',
    gridTemplateColumns: 'repeat(2, 1fr)',
    gap: 12,
    marginTop: 16,
};
const cardStyle: React.CSSProperties = {
    display: 'flex', flexDirection: 'column', gap: 6,
    padding: '16px 18px',
    border: '1px solid var(--center-channel-color-16, rgba(61,60,64,0.16))',
    borderRadius: 6,
    cursor: 'pointer',
    transition: 'background 0.1s',
    textAlign: 'left',
    background: 'transparent',
    color: 'inherit',
    width: '100%',
};

type RequestItem = {
    icon: string;
    title: string;
    description: string;
    action: () => void;
    hidden?: boolean;
};

export const RequestHubModal = () => {
    const dispatch = useDispatch();
    const isOpen = useSelector(isHubModalOpen);
    const channelId = useSelector((state: GlobalState) => getCurrentChannelId(state));
    const teamId = useSelector((state: GlobalState) => getCurrentTeamId(state));
    const alreadyTeamAdmin = useSelector((state: GlobalState) => isCurrentUserTeamAdmin(state, teamId));

    if (!isOpen) {
        return null;
    }

    const close = () => dispatch(closeHubModal());

    const open = (action: () => void) => {
        close();
        // Close hub first so the specific modal renders cleanly on top.
        dispatch(action());
    };

    const items: RequestItem[] = [
        {
            icon: '📢',
            title: 'Request a Channel',
            description: 'Ask for a new public or private channel to be created.',
            action: openRequestModal,
        },
        {
            icon: '⭐',
            title: 'Request Channel Admin',
            description: 'Nominate a Channel Admin for the current channel.',
            action: () => openAdminRequestModal(channelId),
        },
        {
            icon: '🏢',
            title: 'Request a Team',
            description: 'Ask for a new team to be created.',
            action: openTeamCreationModal,
        },
        {
            icon: '🔑',
            title: 'Request Team Admin',
            description: 'Request Team Admin privileges for your current team.',
            action: openTeamAdminRequestModal,
            hidden: alreadyTeamAdmin,
        },
        {
            icon: '🤖',
            title: 'Request a Bot',
            description: 'Request a bot account, access token, or webhook.',
            action: openBotRequestModal,
        },
        {
            icon: '📥',
            title: 'Request Incoming Webhook',
            description: 'Post messages into a channel from an external system.',
            action: openIncomingWebhookModal,
        },
        {
            icon: '📤',
            title: 'Request Outgoing Webhook',
            description: 'Send channel messages to an external endpoint.',
            action: openOutgoingWebhookModal,
        },
    ];

    const visible = items.filter((item) => !item.hidden);

    return (
        <div style={overlayStyle} onClick={close}>
            <div style={dialogStyle} onClick={(e) => e.stopPropagation()}>
                <div style={{display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start'}}>
                    <div>
                        <h3 style={{marginTop: 0, marginBottom: 4}}>{'Request Hub'}</h3>
                        <p style={{margin: 0, opacity: 0.72, fontSize: 14}}>
                            {'Choose a request type. Each request is reviewed by the appropriate admins.'}
                        </p>
                    </div>
                    <button
                        style={{background: 'none', border: 'none', cursor: 'pointer', fontSize: 20, lineHeight: 1, opacity: 0.6, padding: 0}}
                        onClick={close}
                        aria-label='Close'
                    >
                        {'×'}
                    </button>
                </div>

                <div style={gridStyle}>
                    {visible.map((item) => (
                        <button
                            key={item.title}
                            style={cardStyle}
                            onClick={() => open(item.action)}
                            onMouseEnter={(e) => {
                                (e.currentTarget as HTMLButtonElement).style.background =
                                    'var(--center-channel-color-08, rgba(61,60,64,0.08))';
                            }}
                            onMouseLeave={(e) => {
                                (e.currentTarget as HTMLButtonElement).style.background = 'transparent';
                            }}
                        >
                            <span style={{fontSize: 24}}>{item.icon}</span>
                            <span style={{fontWeight: 600, fontSize: 14}}>{item.title}</span>
                            <span style={{fontSize: 13, opacity: 0.7, lineHeight: 1.4}}>{item.description}</span>
                        </button>
                    ))}
                </div>
            </div>
        </div>
    );
};
