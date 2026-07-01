import {expect, test} from '@playwright/experimental-ct-react';
import React from 'react';

import {HeaderIcon} from './HeaderIcon';

test('HeaderIcon renders the plug icon', async ({mount, page}) => {
    await mount(<HeaderIcon/>);
    await expect(page.locator('i')).toHaveClass(/fa-plug/);
});
