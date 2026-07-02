import React, {useEffect, useMemo, useState} from 'react';
import {useDispatch, useSelector} from 'react-redux';

import {fetchPrefixes, submitChannelRequest} from './client';
import type {ChannelPrefix} from './client';
import {MemberPicker} from './MemberPicker';
import {closeRequestModal, getCurrentTeamId, isRequestModalOpen} from './store';
import type {GlobalState} from './store';

// Slugify mirrors the server-side slugify() so the live preview shows
// the exact URL the server will produce. Keep in sync with
// server/request.go's slugify() — same char-class + trim + lower.
function slugifySuffix(input: string): string {
    return input.
        toLowerCase().
        trim().
        replace(/[^a-z0-9]+/g, '-').
        replace(/^-+|-+$/g, '');
}

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

export const RequestChannelModal = () => {
    const dispatch = useDispatch();
    const isOpen = useSelector(isRequestModalOpen);
    const teamId = useSelector((state: GlobalState) => getCurrentTeamId(state));

    const [displayName, setDisplayName] = useState('');
    const [urlName, setUrlName] = useState('');
    const [purpose, setPurpose] = useState('');
    const [channelType, setChannelType] = useState('O');
    const [membersText, setMembersText] = useState('');
    // adminMembersText holds the second picker's selection — users the
    // requester is proposing to be Channel Admins on the new channel.
    // Stored as a comma-separated username string, same shape as
    // membersText, so the payload path is symmetric.
    const [adminMembersText, setAdminMembersText] = useState('');
    const [submitting, setSubmitting] = useState(false);
    const [error, setError] = useState('');
    const [success, setSuccess] = useState('');
    const [prefixes, setPrefixes] = useState<ChannelPrefix[]>([]);
    const [selectedPrefix, setSelectedPrefix] = useState('');

    // Load prefixes on mount. Empty list means the admin hasn't
    // configured the new naming feature — modal falls back to the
    // free-form URL name field.
    useEffect(() => {
        if (!isOpen) {
            return;
        }
        fetchPrefixes().then((list) => {
            setPrefixes(list);
            if (list.length > 0 && !selectedPrefix) {
                setSelectedPrefix(list[0].prefix);
            }
        });
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [isOpen]);

    const usingPrefixList = prefixes.length > 0;

    // Live preview of the final channel URL. In prefix mode: prefix +
    // slugified suffix (or slugified display name when suffix is
    // blank). In legacy mode: slugified URL name or display name.
    const previewURL = useMemo(() => {
        const baseInput = urlName.trim() || displayName;
        const slug = slugifySuffix(baseInput);
        if (usingPrefixList) {
            return selectedPrefix + (slug || 'suffix');
        }
        return slug || 'channel-name';
    }, [urlName, displayName, usingPrefixList, selectedPrefix]);

    if (!isOpen) {
        return null;
    }

    const reset = () => {
        setDisplayName('');
        setUrlName('');
        setPurpose('');
        setChannelType('O');
        setMembersText('');
        setAdminMembersText('');
        setError('');
        setSuccess('');
        setSubmitting(false);
        setSelectedPrefix(prefixes[0]?.prefix ?? '');
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

        const parseCsvUsernames = (raw: string): string[] => raw.
            split(',').
            map((m) => m.trim().replace(/^@/, '')).
            filter((m) => m.length > 0);
        const members = parseCsvUsernames(membersText);
        const adminMembers = parseCsvUsernames(adminMembersText);

        const result = await submitChannelRequest({
            team_id: teamId,
            display_name: displayName.trim(),
            name: urlName.trim(),
            prefix: usingPrefixList ? selectedPrefix : '',
            purpose: purpose.trim(),
            channel_type: channelType,
            members,
            admin_members: adminMembers,
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
                        {usingPrefixList ? (
                            <div style={fieldStyle}>
                                <label htmlFor='cr-prefix'>{'Domain prefix'}</label>
                                <select
                                    id='cr-prefix'
                                    className='form-control'
                                    value={selectedPrefix}
                                    onChange={(e) => setSelectedPrefix(e.target.value)}
                                >
                                    {prefixes.map((p) => (
                                        <option
                                            key={p.prefix}
                                            value={p.prefix}
                                        >
                                            {p.description ? `${p.prefix}  (${p.description})` : p.prefix}
                                        </option>
                                    ))}
                                </select>
                                <small style={{opacity: 0.6}}>{'Choose the category for this channel. The final URL is <prefix><suffix>.'}</small>
                            </div>
                        ) : null}

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
                            <label htmlFor='cr-url-name'>
                                {usingPrefixList ? 'URL suffix (optional)' : 'URL name (optional)'}
                            </label>
                            <input
                                id='cr-url-name'
                                className='form-control'
                                value={urlName}
                                maxLength={64}
                                placeholder={usingPrefixList ? 'The part after the prefix — leave blank to generate from the channel name' : 'Leave blank to generate from the channel name'}
                                onChange={(e) => setUrlName(e.target.value)}
                            />
                            <small style={{opacity: 0.6, display: 'block', marginTop: 4}}>
                                {'Preview: '}<code>{previewURL}</code>
                            </small>
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
                            <MemberPicker
                                value={membersText}
                                teamId={teamId}
                                usernamesToExclude={adminMembersText.split(',').map((s) => s.trim().replace(/^@/, '')).filter(Boolean)}
                                placeholder='Type a name — Tab / Enter to add'
                                onChange={setMembersText}
                            />
                        </div>

                        <div style={fieldStyle}>
                            <label htmlFor='cr-admin-members'>{'Channel Admins to add (optional)'}</label>
                            <MemberPicker
                                value={adminMembersText}
                                teamId={teamId}
                                usernamesToExclude={membersText.split(',').map((s) => s.trim().replace(/^@/, '')).filter(Boolean)}
                                placeholder='Type a name — Tab / Enter to add. Gets Channel Admin role on the new channel.'
                                onChange={setAdminMembersText}
                            />
                            <small style={{opacity: 0.6, display: 'block', marginTop: 4}}>
                                {'These users are promoted to Channel Admin on the newly-created channel (they can pin/manage the channel). Autocomplete is scoped to the current team.'}
                            </small>
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
