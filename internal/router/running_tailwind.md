# How to run tailwind and update other files here

_(for contributors)_

See: https://tailwindcss.com/docs/installation/tailwind-cli

Setup:
Get node and npm

```
npm install
```

The above will install all the packages for tailwind and tailwind typography.
Make sure that node is on your system's path if you're on Windows.

Run the following in the router folder:

```
npx @tailwindcss/cli -i ./static/main.css -o ./static/render.css --watch
```
