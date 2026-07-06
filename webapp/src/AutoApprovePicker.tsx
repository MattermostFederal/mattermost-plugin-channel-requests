import React from 'react';

import {MemberPicker} from './MemberPicker';

// AutoApprovePicker is the MM System Console custom setting for the
// AutoApproveUserIDs field. Under the hood it's the same MemberPicker
// used in the request modal — same @username tab-complete UX, same
// pill display, same debounced autocomplete against the plugin's
// /api/v1/user_autocomplete endpoint. Serializes to a comma-separated
// username string; server-side resolveAutoApproveList() converts
// usernames to user IDs in OnConfigurationChange (so runtime auto-
// approve checks stay O(1) per request).

type Props = {
    id: string;
    label?: string;
    value: string;
    disabled?: boolean;
    onChange: (id: string, value: string) => void;
    setSaveNeeded?: () => void;
};

export const AutoApprovePicker: React.FC<Props> = ({id, value, disabled, onChange, setSaveNeeded}) => {
    return (
        <div style={{padding: '4px 0'}}>
            <MemberPicker
                value={value ?? ''}
                disabled={disabled}
                placeholder='Type a name to search — Tab or Enter to add'
                onChange={(newValue) => {
                    onChange(id, newValue);
                    setSaveNeeded?.();
                }}
            />
            <small style={{opacity: 0.6, display: 'block', marginTop: 4}}>
                {'Users listed here bypass approval — their requests create the channel immediately. Autocomplete is org-wide; the server resolves usernames to IDs on save.'}
            </small>
        </div>
    );
};
