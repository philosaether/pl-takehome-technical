- First principles, take a stab at one of the scaling problems faced by honcho
    - Work units are loaded onto a queue
    - Queue has interesting behavior beyond basic RabbitMQ service
        - 1. We buffer work. Every message, every task has an associated cost; so we buffer the queue until it hits a token cap and then process all at once
        - 2. Strict ordering within a work unit (combination of workspace + session + peer). Within a topic, each item must be processed in order, but multiple topics may be processed in parallel

- So, from first principles:
    - How would you implement a queue system with these behaviors?
    - We want high throughput in production
    - Assume we are serving a fleet of honcho instances, different tenants have isolated instances, isolated resources for each customer
    - Use existing resources or add new ones, but when adding new ones, justify cost

- What they have in postgres is something that emerged during development, not necessarily their desired state
    - So, let's look for a reason to improve on it
    - If we solve this problem better then it's entirely possible it'll actually be used

- He's on WhatsApp, feel free to text questions