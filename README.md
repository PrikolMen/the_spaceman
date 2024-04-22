# The Spaceman
Small and simple discord bot for managing voice channels on your discord server.
This is my first golang project, so yeahh, it can be a bit of a mess. I'll try to tidy it up later.

## Flags
- `-t`: Discord bot token, you know, you'll have to [create a new app](https://discord.com/developers/applications), get a bot token and enter it here.
- `-rp`: Room name pattern, by default is "%s's Room" where `%s` is the global username ( "My Cool Name" )
- `-c`: List of allowed channels separated by a single space ("channelID1 channelID2 channelID3"), these are the channels you need to join for the bot to create a new channel for you.
