import React, {useEffect, useRef, useState} from 'react';

import {searchUsers} from './client';
import type {UserProfile} from './client';

const containerStyle: React.CSSProperties = {position: 'relative'};

const controlStyle: React.CSSProperties = {
    display: 'flex',
    flexWrap: 'wrap',
    gap: 4,
    alignItems: 'center',
    minHeight: 34,
    padding: 4,
};

const chipStyle: React.CSSProperties = {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 4,
    padding: '2px 6px',
    borderRadius: 12,
    background: 'rgba(var(--button-bg-rgb, 28, 88, 217), 0.12)',
    color: 'var(--button-bg, #1c58d9)',
    fontSize: 13,
    lineHeight: '18px',
};

const chipRemoveStyle: React.CSSProperties = {
    cursor: 'pointer',
    border: 'none',
    background: 'transparent',
    color: 'inherit',
    fontSize: 14,
    lineHeight: '14px',
    padding: 0,
};

const inputStyle: React.CSSProperties = {
    flex: '1 0 120px',
    border: 'none',
    outline: 'none',
    background: 'transparent',
    color: 'inherit',
    minWidth: 120,
    padding: '2px 4px',
};

const dropdownStyle: React.CSSProperties = {
    position: 'absolute',
    top: '100%',
    left: 0,
    right: 0,
    zIndex: 1060,
    marginTop: 2,
    maxHeight: 200,
    overflowY: 'auto',
    background: 'var(--center-channel-bg, #fff)',
    color: 'var(--center-channel-color, #3d3c40)',
    border: '1px solid rgba(var(--center-channel-color-rgb, 61, 60, 64), 0.16)',
    borderRadius: 4,
    boxShadow: '0 6px 14px rgba(0, 0, 0, 0.12)',
};

const optionStyle = (active: boolean): React.CSSProperties => ({
    padding: '6px 10px',
    cursor: 'pointer',
    background: active ? 'rgba(var(--button-bg-rgb, 28, 88, 217), 0.08)' : 'transparent',
});

// displayName renders a friendly label for a user, falling back to the username when no real name
// is set.
const displayName = (user: UserProfile): string => {
    const full = [user.first_name, user.last_name].filter(Boolean).join(' ').trim();
    return full || user.nickname || user.username;
};

type Props = {
    teamId: string;
    selected: UserProfile[];
    onChange: (users: UserProfile[]) => void;
};

export const UserMultiSelect = ({teamId, selected, onChange}: Props) => {
    const [term, setTerm] = useState('');
    const [results, setResults] = useState<UserProfile[]>([]);
    const [open, setOpen] = useState(false);
    const [active, setActive] = useState(0);
    const containerRef = useRef<HTMLDivElement>(null);

    // Debounced search against the current instance. Already-selected users are filtered out of the
    // results so they cannot be added twice.
    useEffect(() => {
        const trimmed = term.trim();
        if (!trimmed) {
            setResults([]);
            return undefined;
        }

        let cancelled = false;
        const handle = setTimeout(async () => {
            const users = await searchUsers(teamId, trimmed);
            if (cancelled) {
                return;
            }
            const selectedIds = new Set(selected.map((u) => u.id));
            setResults(users.filter((u) => !selectedIds.has(u.id)));
            setActive(0);
        }, 200);

        return () => {
            cancelled = true;
            clearTimeout(handle);
        };
    }, [term, teamId, selected]);

    // Close the dropdown when clicking outside the control.
    useEffect(() => {
        const onClickOutside = (e: MouseEvent) => {
            if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
                setOpen(false);
            }
        };
        document.addEventListener('mousedown', onClickOutside);
        return () => document.removeEventListener('mousedown', onClickOutside);
    }, []);

    const addUser = (user: UserProfile) => {
        if (!selected.some((u) => u.id === user.id)) {
            onChange([...selected, user]);
        }
        setTerm('');
        setResults([]);
        setActive(0);
    };

    const removeUser = (id: string) => {
        onChange(selected.filter((u) => u.id !== id));
    };

    const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
        if (e.key === 'ArrowDown') {
            e.preventDefault();
            setOpen(true);
            setActive((i) => Math.min(i + 1, results.length - 1));
        } else if (e.key === 'ArrowUp') {
            e.preventDefault();
            setActive((i) => Math.max(i - 1, 0));
        } else if (e.key === 'Enter' && open && results[active]) {
            e.preventDefault();
            addUser(results[active]);
        } else if (e.key === 'Backspace' && !term && selected.length > 0) {
            removeUser(selected[selected.length - 1].id);
        } else if (e.key === 'Escape') {
            setOpen(false);
        }
    };

    return (
        <div
            ref={containerRef}
            style={containerStyle}
        >
            <div
                className='form-control'
                style={controlStyle}
                onClick={() => setOpen(true)}
            >
                {selected.map((user) => (
                    <span
                        key={user.id}
                        style={chipStyle}
                    >
                        {`@${user.username}`}
                        <button
                            type='button'
                            aria-label={`Remove ${user.username}`}
                            style={chipRemoveStyle}
                            onClick={(e) => {
                                e.stopPropagation();
                                removeUser(user.id);
                            }}
                        >
                            {'×'}
                        </button>
                    </span>
                ))}
                <input
                    id='cr-members'
                    style={inputStyle}
                    value={term}
                    placeholder={selected.length ? '' : 'Search members by name or username'}
                    autoComplete='off'
                    onChange={(e) => {
                        setTerm(e.target.value);
                        setOpen(true);
                    }}
                    onFocus={() => setOpen(true)}
                    onKeyDown={onKeyDown}
                />
            </div>

            {open && results.length > 0 ? (
                <div style={dropdownStyle}>
                    {results.map((user, i) => (
                        <div
                            key={user.id}
                            style={optionStyle(i === active)}
                            onMouseDown={(e) => {
                                e.preventDefault();
                                addUser(user);
                            }}
                            onMouseEnter={() => setActive(i)}
                        >
                            <span>{`@${user.username}`}</span>
                            {displayName(user) === user.username ? null : (
                                <span style={{opacity: 0.6, marginLeft: 8}}>{displayName(user)}</span>
                            )}
                        </div>
                    ))}
                </div>
            ) : null}
        </div>
    );
};
