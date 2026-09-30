import React, {useMemo, useState} from 'react';
import {useDispatch, useSelector} from 'react-redux';

import {submitTeamRequest} from './client';
import {MemberPicker, parseUsernames} from './MemberPicker';
import {closeTeamRequestModal, getCurrentTeamId, isTeamRequestModalOpen} from './store';
import type {GlobalState} from './store';

// slugifyName mirrors the server-side slugify() so the live preview shows the
// exact URL the server will produce. Keep in sync with server/request.go's
// slugify() — same char-class + trim + lower.
function slugifyName(input: string): string {
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

export const RequestTeamModal = () => {
    const dispatch = useDispatch();
    const isOpen = useSelector(isTeamRequestModalOpen);

    // The member picker scopes autocomplete to the current team (the server's
    // user-search endpoint requires caller membership of the team_id), so the
    // requester can add people they already share a team with to the new team.
    const teamId = useSelector((state: GlobalState) => getCurrentTeamId(state));

    const [displayName, setDisplayName] = useState('');
    const [urlName, setUrlName] = useState('');
    const [description, setDescription] = useState('');
    const [teamType, setTeamType] = useState('O');
    const [membersText, setMembersText] = useState('');
    const [submitting, setSubmitting] = useState(false);
    const [error, setError] = useState('');
    const [success, setSuccess] = useState('');

    // Live preview of the final team URL: slugified URL name, or slugified
    // display name when the URL field is blank.
    const previewURL = useMemo(() => {
        const slug = slugifyName(urlName.trim() || displayName);
        return slug || 'team-name';
    }, [urlName, displayName]);

    if (!isOpen) {
        return null;
    }

    const reset = () => {
        setDisplayName('');
        setUrlName('');
        setDescription('');
        setTeamType('O');
        setMembersText('');
        setError('');
        setSuccess('');
        setSubmitting(false);
    };

    const close = () => {
        reset();
        dispatch(closeTeamRequestModal());
    };

    const submit = async () => {
        if (!displayName.trim()) {
            setError('A team name is required.');
            return;
        }

        setSubmitting(true);
        setError('');

        try {
            const result = await submitTeamRequest({
                display_name: displayName.trim(),
                name: urlName.trim(),
                description: description.trim(),
                team_type: teamType,
                members: parseUsernames(membersText),
            });

            if (result.error) {
                setError(result.error);
                return;
            }

            setSuccess(result.message || 'Your team request has been submitted.');
        } catch {
            setError('Could not reach the server. Please check your connection and try again.');
        } finally {
            setSubmitting(false);
        }
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
                <h3 style={{marginTop: 0}}>{'Request a Team'}</h3>
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
                            <label htmlFor='tr-display-name'>{'Team name'}</label>
                            <input
                                id='tr-display-name'
                                className='form-control'
                                value={displayName}
                                maxLength={64}
                                placeholder='e.g. Marketing'
                                onChange={(e) => setDisplayName(e.target.value)}
                            />
                        </div>

                        <div style={fieldStyle}>
                            <label htmlFor='tr-url-name'>{'URL name (optional)'}</label>
                            <input
                                id='tr-url-name'
                                className='form-control'
                                value={urlName}
                                maxLength={64}
                                placeholder='Leave blank to generate from the team name'
                                onChange={(e) => setUrlName(e.target.value)}
                            />
                            <small style={{opacity: 0.6, display: 'block', marginTop: 4}}>
                                {'Preview: '}<code>{previewURL}</code>
                            </small>
                        </div>

                        <div style={fieldStyle}>
                            <label htmlFor='tr-description'>{'Description (optional)'}</label>
                            <textarea
                                id='tr-description'
                                className='form-control'
                                value={description}
                                maxLength={250}
                                rows={2}
                                onChange={(e) => setDescription(e.target.value)}
                            />
                        </div>

                        <div style={fieldStyle}>
                            <label htmlFor='tr-type'>{'Visibility'}</label>
                            <select
                                id='tr-type'
                                className='form-control'
                                value={teamType}
                                onChange={(e) => setTeamType(e.target.value)}
                            >
                                <option value='O'>{'Open (anyone on the server can join)'}</option>
                                <option value='I'>{'Invite only'}</option>
                            </select>
                        </div>

                        <div style={fieldStyle}>
                            <label htmlFor='tr-members'>{'Members to add (optional)'}</label>
                            <MemberPicker
                                value={membersText}
                                teamId={teamId}
                                placeholder='Type a name — Tab / Enter to add'
                                onChange={setMembersText}
                            />
                            <small style={{opacity: 0.6, display: 'block', marginTop: 4}}>
                                {'These users are added to the team once it’s approved. You’re added as a Team Admin automatically.'}
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
