import React, {useCallback, useEffect, useRef, useState} from 'react';

import {autocompleteUsers, fetchUsersByIDs} from './client';
import type {UserSummary} from './client';
import {MemberPicker} from './MemberPicker';

// AutoApprovePicker is the MM System Console custom setting for the
// AutoApproveUserIDs field. It reuses the request modal's MemberPicker for
// the @username tab-complete and pill UX, but the setting itself stores
// comma-separated user IDs: a username can be freed by a rename and claimed
// by someone else, who would then inherit auto-approval. The picker shows
// usernames and translates to and from IDs at the edges.
//
// Search goes through core's /users/autocomplete: the plugin's own
// endpoint is team-scoped, and the System Console has no team.

type Props = {
    id: string;
    label?: string;
    value: string;
    disabled?: boolean;
    onChange: (id: string, value: string) => void;
    setSaveNeeded?: () => void;
};

function parseIDs(v: string): string[] {
    return (v ?? '').split(/[\s,;]+/).filter((s) => s.length > 0);
}

export const AutoApprovePicker: React.FC<Props> = ({id, value, disabled, onChange, setSaveNeeded}) => {
    // username <-> id for every user seen in the saved value or a search.
    const idByUsername = useRef<Map<string, string>>(new Map());
    const usernameByID = useRef<Map<string, string>>(new Map());
    const [usernames, setUsernames] = useState('');
    const [loadError, setLoadError] = useState('');

    const remember = (users: UserSummary[]) => {
        for (const u of users) {
            idByUsername.current.set(u.username, u.id);
            usernameByID.current.set(u.id, u.username);
        }
    };

    // Resolve the saved IDs to usernames for display, fetching only IDs not
    // already known (the picker's own edits round-trip through `value`).
    useEffect(() => {
        let cancelled = false;
        const ids = parseIDs(value);
        const unknown = ids.filter((uid) => !usernameByID.current.has(uid));
        const show = () => {
            setUsernames(ids.map((uid) => usernameByID.current.get(uid)).filter(Boolean).join(', '));
        };
        if (unknown.length === 0) {
            show();
            return undefined;
        }
        fetchUsersByIDs(unknown).then((users) => {
            if (cancelled) {
                return;
            }
            remember(users);
            setLoadError('');
            show();
        }).catch(() => {
            if (!cancelled) {
                setLoadError('Could not load the saved users. Reload the page before editing this list.');
            }
        });
        return () => {
            cancelled = true;
        };
    }, [value]);

    const search = useCallback(async (term: string) => {
        const users = await autocompleteUsers(term);
        remember(users);
        return users;
    }, []);

    return (
        <div style={{padding: '4px 0'}}>
            <MemberPicker
                value={usernames}
                disabled={disabled || Boolean(loadError)}
                placeholder='Type a name to search — Tab or Enter to add'
                searchUsers={search}
                onChange={(newValue) => {
                    const ids = newValue.
                        split(',').
                        map((s) => idByUsername.current.get(s.trim().replace(/^@/, ''))).
                        filter((uid): uid is string => Boolean(uid));
                    setUsernames(newValue);
                    onChange(id, ids.join(','));
                    setSaveNeeded?.();
                }}
            />
            {loadError && <small style={{color: 'var(--error-text, #d24b4e)', display: 'block', marginTop: 4}}>{loadError}</small>}
            <small style={{opacity: 0.6, display: 'block', marginTop: 4}}>
                {'Users listed here bypass approval — their requests create the channel immediately. Saved as user IDs, so renaming an account never changes who is on the list.'}
            </small>
        </div>
    );
};
