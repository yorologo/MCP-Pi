import assert from 'node:assert/strict';

const listeners = {};
const classes = new Set();
const activeElement = {};
let focused = false;

const navigation = {
  classList: {
    add: value => classes.add(value),
    contains: value => classes.has(value),
    toggle: value => classes.has(value) ? classes.delete(value) : classes.add(value),
  },
  contains: element => element === activeElement,
};
const navButton = {
  addEventListener: () => {},
  classList: { add: () => {}, remove: () => {} },
  setAttribute: () => {},
  focus: () => { focused = true; },
};

const projectOptions = [
  { value: '*', dataset: {}, hidden: false, disabled: false },
  { value: 'shared', dataset: { targetId: 'target-a' }, hidden: false, disabled: false },
  { value: 'shared', dataset: { targetId: 'target-b' }, hidden: false, disabled: false },
];
const projectSelect = {
  options: projectOptions,
  selectedIndex: 1,
  value: 'shared',
};
const targetHandlers = {};
const targetSelect = {
  dataset: { projectSelect: 'project-select' },
  value: 'target-a',
  addEventListener: (event, callback) => { targetHandlers[event] = callback; },
};

globalThis.document = {
  activeElement,
  addEventListener: (event, callback) => { listeners[event] = callback; },
  getElementById: id => id === 'primary-navigation' ? navigation : (id === 'project-select' ? projectSelect : null),
  querySelector: () => navButton,
  querySelectorAll: selector => selector === '[data-project-select]' ? [targetSelect] : [],
};

await import('./static/app.js');
listeners.DOMContentLoaded();
listeners.keydown({ key: 'Escape' });

assert.equal(focused, true, 'Escape returns focus to the mobile navigation toggle');
assert.equal(classes.has('hidden'), true, 'Escape closes the mobile navigation');


assert.equal(projectOptions[0].disabled, false, 'Wildcard project always remains available');
assert.equal(projectOptions[1].disabled, false, 'Selected Target project remains available');
assert.equal(projectOptions[2].disabled, true, 'Other Target project is disabled');

targetSelect.value = 'target-b';
targetHandlers.change();
assert.equal(projectOptions[1].disabled, true, 'Previous Target project is disabled after Target change');
assert.equal(projectOptions[2].disabled, false, 'New Target project becomes available');
assert.equal(projectSelect.value, '*', 'Invalid Project selection resets to wildcard');

projectSelect.selectedIndex = 0;
targetSelect.value = '*';
targetHandlers.change();
assert.equal(projectOptions[1].disabled, true, 'Global Target scope does not allow a specific Project');
assert.equal(projectOptions[2].disabled, true, 'Global Target scope hides all specific Projects');
