#!/usr/bin/env bash



# get the user that owns our app here
APP_PATH="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APP_USER=$(stat -c '%U' "$APP_PATH")

# make sure we own it
chown -R $APP_USER:$APP_USER $APP_PATH*;

# make sure node modules are up to date, and we build the css
rm -rf $APP_PATH/node_modules
npm install --prefix "$APP_PATH"
npm run build --prefix "$APP_PATH"

# make sure we own it one last time
chown -R $APP_USER:$APP_USER $APP_PATH*;

# reset permissions
find $APP_PATH -type d -exec chmod 755 {} \;
find $APP_PATH -type f -exec chmod 644 {} \;
chmod +x $APP_PATH/build.sh


