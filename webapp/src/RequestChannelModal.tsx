import React, {useState} from 'react';
import {useDispatch, useSelector} from 'react-redux';

import {submitChannelRequest} from './client';
import type {UserProfile} from './client';
import {closeRequestModal, getCurrentTeamId, getCurrentUser, isRequestModalOpen} from './store';
import type {GlobalState} from './store';
import {UserMultiSelect} from './UserMultiSelect';

const overlayStyle: React.CSSProperties = {
    position: 'fixed',
    inset: 0,
    background: 'rgba(0, 0, 0, 0.5)',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    zIndex: 1050,
};

const dialogStyle: React.CSSProperties = {
    background: 'var(--center-channel-bg, #fff)',
    color: 'var(--center-channel-color, #3d3c40)',
    borderRadius: 8,
    width: 512,
    maxWidth: '90vw',
    maxHeight: '90vh',
    overflowY: 'auto',
    padding: 24,
    boxShadow: '0 8px 24px rgba(0, 0, 0, 0.24)',
};

const fieldStyle: React.CSSProperties = {marginBottom: 16};

const addSelfStyle: React.CSSProperties = {
    padding: 0,
    marginTop: 6,
    fontSize: 13,
    background: 'transparent',
    border: 'none',
    color: 'var(--link-color, #386fe5)',
    cursor: 'pointer',
};

export const RequestChannelModal = () => {
    const dispatch = useDispatch();
    const isOpen = useSelector(isRequestModalOpen);
    const teamId = useSelector((state: GlobalState) => getCurrentTeamId(state));
    const currentUser = useSelector((state: GlobalState) => getCurrentUser(state));

    const [displayName, setDisplayName] = useState('');
    const [urlName, setUrlName] = useState('');
    const [purpose, setPurpose] = useState('');
    const [channelType, setChannelType] = useState('O');
    const [members, setMembers] = useState<UserProfile[]>([]);
    const [channelAdmins, setChannelAdmins] = useState<UserProfile[]>([]);
    const [submitting, setSubmitting] = useState(false);
    const [error, setError] = useState('');
    const [success, setSuccess] = useState('');

    if (!isOpen) {
        return null;
    }

    const reset = () => {
        setDisplayName('');
        setUrlName('');
        setPurpose('');
        setChannelType('O');
        setMembers([]);
        setChannelAdmins([]);
        setError('');
        setSuccess('');
        setSubmitting(false);
    };

    const close = () => {
        reset();
        dispatch(closeRequestModal());
    };

    const submit = async () => {
        if (!displayName.trim()) {
            setError('A channel name is required.');
            return;
        }

        setSubmitting(true);
        setError('');

        const result = await submitChannelRequest({
            team_id: teamId,
            display_name: displayName.trim(),
            name: urlName.trim(),
            purpose: purpose.trim(),
            channel_type: channelType,
            members: members.map((u) => u.username),
            channel_admins: channelAdmins.map((u) => u.username),
        });

        setSubmitting(false);

        if (result.error) {
            setError(result.error);
            return;
        }

        setSuccess(result.message || 'Your channel request has been submitted.');
    };

    return (
        <div
            style={overlayStyle}
            onClick={close}
        >
            <div
                style={dialogStyle}
                onClick={(e) => e.stopPropagation()}
            >
                <h3 style={{marginTop: 0}}>{'Request a Channel'}</h3>
                <p style={{opacity: 0.72}}>{'Your request will be sent to an admin for approval.'}</p>

                {success ? (
                    <div>
                        <div
                            className='alert alert-success'
                            style={{marginBottom: 16}}
                        >
                            {success}
                        </div>
                        <div style={{textAlign: 'right'}}>
                            <button
                                className='btn btn-primary'
                                onClick={close}
                            >
                                {'Close'}
                            </button>
                        </div>
                    </div>
                ) : (
                    <div>
                        <div style={fieldStyle}>
                            <label htmlFor='cr-display-name'>{'Channel name'}</label>
                            <input
                                id='cr-display-name'
                                className='form-control'
                                value={displayName}
                                maxLength={64}
                                placeholder='e.g. Marketing Team'
                                onChange={(e) => setDisplayName(e.target.value)}
                            />
                        </div>

                        <div style={fieldStyle}>
                            <label htmlFor='cr-url-name'>{'URL name (optional)'}</label>
                            <input
                                id='cr-url-name'
                                className='form-control'
                                value={urlName}
                                maxLength={64}
                                placeholder='Leave blank to generate from the channel name'
                                onChange={(e) => setUrlName(e.target.value)}
                            />
                        </div>

                        <div style={fieldStyle}>
                            <label htmlFor='cr-purpose'>{'Purpose (optional)'}</label>
                            <textarea
                                id='cr-purpose'
                                className='form-control'
                                value={purpose}
                                maxLength={250}
                                rows={2}
                                onChange={(e) => setPurpose(e.target.value)}
                            />
                        </div>

                        <div style={fieldStyle}>
                            <label htmlFor='cr-type'>{'Visibility'}</label>
                            <select
                                id='cr-type'
                                className='form-control'
                                value={channelType}
                                onChange={(e) => setChannelType(e.target.value)}
                            >
                                <option value='O'>{'Public'}</option>
                                <option value='P'>{'Private'}</option>
                            </select>
                        </div>

                        <div style={fieldStyle}>
                            <label htmlFor='cr-members'>{'Members to add (optional)'}</label>
                            <UserMultiSelect
                                teamId={teamId}
                                selected={members}
                                onChange={setMembers}
                            />
                        </div>

                        <div style={fieldStyle}>
                            <label htmlFor='cr-admins'>{'Channel admins (optional)'}</label>
                            <UserMultiSelect
                                teamId={teamId}
                                selected={channelAdmins}
                                onChange={setChannelAdmins}
                                inputId='cr-admins'
                                placeholder='Search users to make channel admins'
                            />
                            {currentUser && !channelAdmins.some((u) => u.id === currentUser.id) ? (
                                <button
                                    type='button'
                                    style={addSelfStyle}
                                    onClick={() => setChannelAdmins([...channelAdmins, currentUser])}
                                >
                                    {'+ Add me as a channel admin'}
                                </button>
                            ) : null}
                        </div>

                        {error ? (
                            <div
                                className='alert alert-danger'
                                style={{marginBottom: 16}}
                            >
                                {error}
                            </div>
                        ) : null}

                        <div style={{textAlign: 'right'}}>
                            <button
                                className='btn btn-tertiary'
                                style={{marginRight: 8}}
                                onClick={close}
                                disabled={submitting}
                            >
                                {'Cancel'}
                            </button>
                            <button
                                className='btn btn-primary'
                                onClick={submit}
                                disabled={submitting}
                            >
                                {submitting ? 'Submitting…' : 'Submit request'}
                            </button>
                        </div>
                    </div>
                )}
            </div>
        </div>
    );
};
