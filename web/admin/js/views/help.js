import { mountView } from '../components/view-shell.js';

const steps = [
  {
    title: 'Register the service',
    module: 'Services',
    route: '/services',
    summary: 'Create the API identity, a deployable version, and at least one server that can receive traffic.',
    tasks: [
      'Choose Register service and enter its stable name, display name, and owner.',
      'Select the service and add a version with its request timeout, body limit, and default health path.',
      'Select the version and add one or more literal-IP upstreams with protocol, port, and weight.',
      'Ensure each health endpoint accepts an unauthenticated GET and returns a 2xx or 3xx response.',
    ],
  },
  {
    title: 'Allocate a listener port',
    module: 'Ports & listeners',
    route: '/ports',
    summary: 'Bind the API version to a client-facing port and define what requests the gateway accepts.',
    tasks: [
      'Choose Allocate listener and select the service and version.',
      'Choose or create the required access rule.',
      'Set allowed HTTP methods, accepted content types, and any required traffic policies.',
      'Keep the allocated client address from Connection details for the final client setup.',
    ],
  },
  {
    title: 'Register the consumer and issue a key',
    module: 'Consumers & keys',
    route: '/consumers',
    summary: 'Create the client identity, grant the listener’s required access, and issue its one-time credential.',
    tasks: [
      'Add an API client and assign the access rule required by the listener.',
      'Select the client, choose Create key, and add a recognizable key label and optional expiration.',
      'Copy the raw key immediately into the client secret manager; Shinmon cannot display it again.',
      'Configure protected client requests with X-API-Key: <issued-api-key>.',
    ],
  },
  {
    title: 'Publish the configuration',
    module: 'Configurations',
    route: '/configurations',
    summary: 'Capture the management changes in an immutable snapshot and make that snapshot live on the gateways.',
    tasks: [
      'Choose Create snapshot after the service, upstream, listener, access, and policy settings are complete.',
      'Select the draft and validate it.',
      'Collect approvals from distinct actors when approvals are required.',
      'Activate the validated snapshot and confirm that gateway replicas load its version.',
    ],
  },
  {
    title: 'Connect and verify the client',
    module: 'Ports & listeners',
    route: '/ports',
    summary: 'Distribute only the published client address and issued key, then exercise an allowed request.',
    tasks: [
      'Open Connection details and confirm the listener is active and has a published configuration.',
      'Send the request to the client address and listener port with the X-API-Key header.',
      'Confirm an allowed request succeeds and a request without the key returns 401 unless the route is explicitly public.',
      'Check Gateway health and Audit if publication or authentication does not behave as expected.',
    ],
  },
];

export async function render({ navigate }) {
  const { content } = mountView('Help & setup guide', 'Step-by-step instructions for publishing an API and connecting an authorized client.', [], 'help');
  const notice = document.createElement('section'); notice.className = 'panel setup-guide-notice';
  notice.innerHTML = '<p class="section-kicker">Before you start</p><h2>Prepare the upstream and access plan</h2><p>Confirm that each upstream IP is allowed by the gateway configuration, its health endpoint is reachable, and you know which client access rule the listener should require.</p><p><strong>Publication rule:</strong> service, listener, consumer, and policy edits are saved immediately, but gateway traffic changes only after you activate a new configuration.</p>';
  const list = document.createElement('ol'); list.className = 'setup-guide-steps';
  steps.forEach((step, index) => {
    const item = document.createElement('li'); item.className = 'setup-guide-step';
    const number = document.createElement('span'); number.className = 'setup-guide-number'; number.textContent = String(index + 1);
    const card = document.createElement('section'); card.className = 'panel';
    const heading = document.createElement('div'); heading.className = 'setup-guide-heading';
    const copy = document.createElement('div'); const title = document.createElement('h2'); title.textContent = step.title; const module = document.createElement('p'); module.className = 'section-kicker'; module.textContent = step.module; const summary = document.createElement('p'); summary.textContent = step.summary; copy.append(module, title, summary);
    const button = document.createElement('button'); button.type = 'button'; button.className = 'secondary-button'; button.textContent = `Open ${step.module}`; button.addEventListener('click', () => navigate(step.route));
    heading.append(copy, button); const tasks = document.createElement('ul'); tasks.className = 'setup-guide-tasks'; step.tasks.forEach((task) => { const taskItem = document.createElement('li'); taskItem.textContent = task; tasks.append(taskItem); });
    card.append(heading, tasks); item.append(number, card); list.append(item);
  });
  content.append(notice, list);
  return () => {};
}
