import React, {useEffect, useRef, useState} from 'react';

import manifest from 'manifest';

// MemberPicker replaces the "Comma-separated usernames" text field in
// the request modal with a real autocomplete-driven picker.
//
// UX:
//   - Type in the input -> debounced fetch to /api/v1/user_autocomplete
//   - Matching users show as a dropdown below the input
//   - Click a user (or Enter on a highlighted one) -> add to the pill list
//   - "×" on a pill removes them
//   - The picker exposes value as a comma-separated username string via
//     onChange, so the parent Modal doesn't have to know about pills
//
// Debounce is 200ms — snappy enough to feel live, sparse enough not to
// hammer the endpoint on every keystroke.

type User = {
    id: string;
    username: string;
    nickname?: string;
    first_name?: string;
    last_name?: string;
};

type Props = {
    value: string; // comma-separated usernames
    disabled?: boolean;
    onChange: (value: string) => void;
    placeholder?: string;
};

// Parse comma-separated usernames -> array of trimmed non-empty names.
function parseUsernames(v: string): string[] {
    return v.
        split(',').
        map((s) => s.trim().replace(/^@/, '')).
        filter((s) => s.length > 0);
}

function displayName(u: User): string {
    const first = u.first_name ?? '';
    const last = u.last_name ?? '';
    const full = `${first} ${last}`.trim();
    if (full) {
        return `${u.username}  —  ${full}`;
    }
    if (u.nickname) {
        return `${u.username}  —  ${u.nickname}`;
    }
    return u.username;
}

export const MemberPicker: React.FC<Props> = ({value, disabled, onChange, placeholder}) => {
    const [selected, setSelected] = useState<string[]>(() => parseUsernames(value));
    const [query, setQuery] = useState('');
    const [candidates, setCandidates] = useState<User[]>([]);
    const [showDropdown, setShowDropdown] = useState(false);
    const [highlightIndex, setHighlightIndex] = useState(0);
    const containerRef = useRef<HTMLDivElement>(null);

    // Re-sync when parent value changes externally.
    useEffect(() => {
        setSelected(parseUsernames(value));
    }, [value]);

    // Debounced autocomplete fetch.
    useEffect(() => {
        if (!query.trim()) {
            setCandidates([]);
            return;
        }
        const q = query.trim();
        const timer = window.setTimeout(async () => {
            try {
                const response = await fetch(
                    `/plugins/${manifest.id}/api/v1/user_autocomplete?q=${encodeURIComponent(q)}`,
                    {credentials: 'same-origin', headers: {'X-Requested-With': 'XMLHttpRequest'}},
                );
                if (!response.ok) {
                    setCandidates([]);
                    return;
                }
                const users: User[] = await response.json();
                // Filter out anyone already selected.
                setCandidates(users.filter((u) => !selected.includes(u.username)));
                setHighlightIndex(0);
            } catch {
                setCandidates([]);
            }
        }, 200);
        return () => window.clearTimeout(timer);
    }, [query, selected]);

    // Close dropdown when clicking outside.
    useEffect(() => {
        const handler = (e: MouseEvent) => {
            if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
                setShowDropdown(false);
            }
        };
        document.addEventListener('mousedown', handler);
        return () => document.removeEventListener('mousedown', handler);
    }, []);

    const commit = (next: string[]) => {
        setSelected(next);
        onChange(next.join(', '));
    };

    const addUser = (username: string) => {
        if (!username || selected.includes(username)) {
            return;
        }
        commit([...selected, username]);
        setQuery('');
        setCandidates([]);
    };

    const removeUser = (username: string) => {
        commit(selected.filter((u) => u !== username));
    };

    const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
        if (!showDropdown || candidates.length === 0) {
            if (e.key === 'Enter' && query.trim()) {
                e.preventDefault();
                addUser(query.trim().replace(/^@/, ''));
            }
            return;
        }
        if (e.key === 'ArrowDown') {
            e.preventDefault();
            setHighlightIndex((i) => Math.min(i + 1, candidates.length - 1));
        } else if (e.key === 'ArrowUp') {
            e.preventDefault();
            setHighlightIndex((i) => Math.max(i - 1, 0));
        } else if (e.key === 'Enter') {
            e.preventDefault();
            const u = candidates[highlightIndex];
            if (u) {
                addUser(u.username);
            }
        } else if (e.key === 'Escape') {
            setShowDropdown(false);
        } else if (e.key === 'Backspace' && !query && selected.length > 0) {
            // Backspace on empty input removes the last selected user.
            commit(selected.slice(0, -1));
        }
    };

    return (
        <div
            ref={containerRef}
            style={{position: 'relative'}}
        >
            <div
                style={{
                    display: 'flex',
                    flexWrap: 'wrap',
                    gap: 4,
                    padding: 6,
                    minHeight: 34,
                    border: '1px solid var(--center-channel-color-rgb, rgba(0,0,0,0.16))',
                    borderRadius: 4,
                    background: 'var(--center-channel-bg, #fff)',
                }}
                onClick={() => {
                    setShowDropdown(true);
                    (containerRef.current?.querySelector('input') as HTMLInputElement | null)?.focus();
                }}
            >
                {selected.map((username) => (
                    <span
                        key={username}
                        style={{
                            display: 'inline-flex',
                            alignItems: 'center',
                            gap: 4,
                            padding: '2px 8px',
                            borderRadius: 12,
                            background: 'rgba(28, 88, 217, 0.12)',
                            fontSize: 13,
                        }}
                    >
                        {'@' + username}
                        <button
                            type='button'
                            style={{
                                border: 'none',
                                background: 'transparent',
                                cursor: 'pointer',
                                padding: 0,
                                fontSize: 14,
                                lineHeight: 1,
                            }}
                            disabled={disabled}
                            onClick={(e) => {
                                e.stopPropagation();
                                removeUser(username);
                            }}
                        >
                            {'×'}
                        </button>
                    </span>
                ))}
                <input
                    style={{
                        flex: 1,
                        minWidth: 120,
                        border: 'none',
                        outline: 'none',
                        background: 'transparent',
                        fontSize: 14,
                        padding: '2px 4px',
                    }}
                    value={query}
                    placeholder={selected.length === 0 ? (placeholder ?? 'Type a name...') : ''}
                    disabled={disabled}
                    onChange={(e) => {
                        setQuery(e.target.value);
                        setShowDropdown(true);
                    }}
                    onFocus={() => setShowDropdown(true)}
                    onKeyDown={handleKeyDown}
                />
            </div>

            {showDropdown && candidates.length > 0 ? (
                <div
                    style={{
                        position: 'absolute',
                        top: '100%',
                        left: 0,
                        right: 0,
                        marginTop: 2,
                        background: 'var(--center-channel-bg, #fff)',
                        border: '1px solid rgba(0, 0, 0, 0.12)',
                        borderRadius: 4,
                        boxShadow: '0 4px 12px rgba(0, 0, 0, 0.16)',
                        maxHeight: 240,
                        overflowY: 'auto',
                        zIndex: 1100,
                    }}
                >
                    {candidates.map((u, i) => (
                        <div
                            key={u.id}
                            style={{
                                padding: '6px 10px',
                                cursor: 'pointer',
                                background: i === highlightIndex ? 'rgba(28, 88, 217, 0.08)' : 'transparent',
                                fontSize: 14,
                            }}
                            onMouseEnter={() => setHighlightIndex(i)}
                            onMouseDown={(e) => {
                                // mousedown fires before blur so we can select without the outside-click closer eating it.
                                e.preventDefault();
                                addUser(u.username);
                            }}
                        >
                            {displayName(u)}
                        </div>
                    ))}
                </div>
            ) : null}
        </div>
    );
};
