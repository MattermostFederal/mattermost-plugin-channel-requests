import manifest from 'manifest';
import React from 'react';
import {Provider} from 'react-redux';
import {combineReducers, createStore} from 'redux';

import {RequestTeamModal} from './RequestTeamModal';
import pluginReducer from './store';

// TeamModalHarness builds a self-contained Redux store in the browser (Playwright
// CT can't pass a store as a prop) and renders the modal. `open` seeds the
// team-modal state. Lives in its own module because CT can only mount components
// imported from non-test files ("stories").
export const TeamModalHarness = ({open}: {open: boolean}) => {
    const store = React.useMemo(() => createStore(
        combineReducers({
            [`plugins-${manifest.id}`]: pluginReducer as any,
            entities: (s = {teams: {currentTeamId: 'team1'}}) => s,
        }),
        {
            [`plugins-${manifest.id}`]: {modalOpen: false, teamModalOpen: open, adminModalChannelId: null},
            entities: {teams: {currentTeamId: 'team1'}},
        } as any,
    ), []);
    return (
        <Provider store={store}>
            <RequestTeamModal/>
        </Provider>
    );
};
