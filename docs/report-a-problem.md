# Report a problem or share an idea

The bug button at the bottom of the sidebar opens a small window for telling me about a problem or an idea. You'll also find it in the profile menu and as **Copy for support** under **Settings > System > Logs and errors**.

## What goes in it

- **A problem** or **An idea**.
- A short **title** and a few sentences on what happened (or what you'd like). For a problem, the steps to make it happen again help most.
- An optional **screenshot**. **Take a screenshot** moves the window out of the way and shows a small bar: go to the part of Mediarium you want to show, press **Take screenshot** on the bar, and you're back in the form with the picture attached. Your browser asks first whether Mediarium may capture the tab (Chrome and Edge offer this tab straight away; Firefox and Safari ask which screen, window or tab). This needs Mediarium opened over https, or on `localhost`. **Add a picture** takes a PNG, JPEG or WebP file, and you can also paste one with Ctrl+V. Pictures are made smaller and saved again as a plain JPEG in your browser before they go anywhere, so only the picture itself is sent.
- For administrators, **Include the support report**: the same text as **Copy for support** (your version, setup warnings, recent problems and the last 500 log lines, with passwords, keys and tokens taken out). **See what is included** shows it before you send. Other accounts can't read the log, so their reports go without it.
- An optional name or way to reach you, such as `u/name` on Reddit, `@name` on GitHub or an email. It's only used to reply, or to thank you by name in the changelog and on the [People who helped](community.md) page if it leads to a change.

## Two ways to send it

**Send privately** sends it from your Mediarium server to mediarium.app, where it's kept privately and emailed to me. Nobody else sees it, and nothing is posted anywhere. You get a reference number back.

**Open on GitHub** opens a new GitHub issue in another tab with the title, your description and the version already filled in. You sign in to GitHub, check it and press **Create**. The support report is never put in the link, because a link stays in your browser history and in the logs of anything in between, even if you never post the issue. Mediarium copies the report for you instead, so you paste it into the Support report box. GitHub issues are public, so read the text and the report for anything private, such as folder names, before posting. The screenshot can't go in the link either. The window has buttons to copy the report again, copy the screenshot or download it, so you can paste or drag them into the GitHub page.

## Limits

To keep spam out, private reports have limits:

- At most 5 reports an hour from one person, and 15 an hour from everyone on one Mediarium server together (an administrator can always send their own 5), and a few more per day at mediarium.app.
- Each report carries a small proof-of-work stamp the server works out before sending (a fraction of a second), so sending thousands is expensive.
- Title up to 150 characters, description up to 8,000, screenshot up to 5 MB and 8,000 pixels a side. Only PNG and JPEG pictures are accepted, and mediarium.app checks and saves each one again before keeping it.

If mediarium.app can't be reached (no internet, or a firewall), the window says so: use **Open on GitHub** instead. Private reports go through the same proxy as indexers when you've set one under Settings > Downloading > VPN and proxy > Proxy.
