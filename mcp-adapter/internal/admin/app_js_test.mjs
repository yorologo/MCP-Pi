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

const tooltipHandlers = {};
const tooltip = {
  hidden: true,
  addEventListener: (event, callback) => { tooltipHandlers['tooltip:' + event] = callback; },
  contains: () => false,
};
const tooltipTrigger = {
  addEventListener: (event, callback) => { tooltipHandlers[event] = callback; },
  getAttribute: name => name === 'aria-describedby' ? 'grant-tooltip' : null,
  setAttribute: () => {},
};

globalThis.document = {
  activeElement,
  addEventListener: (event, callback) => { listeners[event] = callback; },
  getElementById: id => id === 'primary-navigation'
    ? navigation
    : (id === 'project-select' ? projectSelect : (id === 'grant-tooltip' ? tooltip : null)),
  querySelector: () => navButton,
  querySelectorAll: selector => {
    if (selector === '[data-project-select]') return [targetSelect];
    if (selector === '[data-tooltip-trigger]') return [tooltipTrigger];
    return [];
  },
};

await import('./static/app.js');
listeners.DOMContentLoaded();
listeners.keydown({ key: 'Escape' });

assert.equal(typeof tooltipHandlers.focus, 'function', 'Tooltip supports keyboard focus');
assert.equal(typeof tooltipHandlers.click, 'function', 'Tooltip supports touch/click activation');
assert.equal(typeof tooltipHandlers.mouseenter, 'function', 'Tooltip supports hover activation');
tooltipHandlers.focus();
await new Promise(resolve => setTimeout(resolve, 0));
assert.equal(tooltip.hidden, false, 'Keyboard focus opens the tooltip');
listeners.keydown({ key: 'Escape' });
assert.equal(tooltip.hidden, true, 'Escape closes an open tooltip');

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


const presetModels = [
  { name: 'Read only', capabilities: ['read'] },
  { name: 'Structured operator', capabilities: ['read', 'write', 'tasks'] },
  { name: 'Trusted shell', capabilities: ['read', 'write', 'tasks', 'target_shell'] },
  { name: 'Privileged shell', capabilities: ['read', 'write', 'tasks', 'target_shell', 'target_admin'], highImpact: true },
  { name: 'Gateway diagnostics', capabilities: ['status', 'doctor'] },
  { name: 'Gateway maintenance', capabilities: ['status', 'doctor', 'backup', 'maintenance'] },
  { name: 'Gateway administrator', capabilities: ['admin'] },
  { name: 'All ordinary access', capabilities: ['*'], highImpact: true },
];

const { applyGrantPreset, isHighImpactGrantSelection, summarizeGrantAccess } = globalThis.MCPGrantUI;

const capabilityInputs = [
  { value: 'read', checked: false },
  { value: 'write', checked: false },
  { value: 'tasks', checked: false },
  { value: 'target_shell', checked: false },
  { value: 'target_admin', checked: false },
  { value: 'doctor', checked: false },
];
applyGrantPreset(capabilityInputs, ['read', 'write', 'tasks', 'target_shell'], true);
assert.deepEqual(
  capabilityInputs.filter(input => input.checked).map(input => input.value),
  ['read', 'write', 'tasks', 'target_shell'],
  'Trusted shell preset selects only its real capabilities',
);
applyGrantPreset(capabilityInputs, ['read', 'write', 'tasks'], false);
assert.deepEqual(
  capabilityInputs.filter(input => input.checked).map(input => input.value),
  ['target_shell'],
  'Clearing an overlapping preset updates the same underlying capability state',
);

assert.equal(isHighImpactGrantSelection(['read', 'target_admin']), true, 'target_admin is high impact');
assert.equal(isHighImpactGrantSelection(['*']), true, 'wildcard ordinary access is high impact');
assert.equal(isHighImpactGrantSelection(['read', 'write']), false, 'ordinary structured capabilities are not high impact');

assert.deepEqual(
  summarizeGrantAccess(['read', 'write', 'tasks', 'target_shell'], presetModels).map(item => item.name),
  ['Trusted shell'],
  'Summary prefers the most specific preset over contained presets',
);
assert.deepEqual(
  summarizeGrantAccess(['read', 'write', 'tasks', 'target_shell', 'doctor'], presetModels).map(item => item.name),
  ['Trusted shell', 'doctor'],
  'Summary keeps residual capabilities visible',
);
assert.deepEqual(
  summarizeGrantAccess(['read', 'write', 'tasks', 'target_shell', 'target_admin'], presetModels).map(item => item.name),
  ['Privileged shell'],
  'Privileged shell summarizes its full capability set',
);
