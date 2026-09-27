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

globalThis.document = {
  activeElement,
  addEventListener: (event, callback) => { listeners[event] = callback; },
  getElementById: () => navigation,
  querySelector: () => navButton,
  querySelectorAll: () => [],
};

await import('./static/app.js');
listeners.DOMContentLoaded();
listeners.keydown({ key: 'Escape' });

assert.equal(focused, true, 'Escape returns focus to the mobile navigation toggle');
assert.equal(classes.has('hidden'), true, 'Escape closes the mobile navigation');
