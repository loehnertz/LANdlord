# LANdlord user guide

This guide is for the person whose internet is acting up. You don't need to know anything about networks.

## Before you start

- Use the laptop where the problems happen, in the rooms where you normally use it.
- Keep the laptop plugged in if you can. LANdlord stops the laptop from going to sleep while it records, but closing the lid may still put it to sleep.
- Use the internet normally: calls, streaming, browsing. LANdlord needs the problems to happen while it's watching.

## 1. Start LANdlord

1. Unzip the file you received and double-click `landlord.exe`.
2. Windows may show a blue window saying "Windows protected your PC". This appears for programs that aren't code-signed. Click **More info**, then **Run anyway**.
3. Windows asks "Do you want to allow this app to make changes to your device?". Click **Yes** if you can; LANdlord then also reads Windows' Wi-Fi history. **No** is fine too. LANdlord never changes any settings either way.

A page opens in your browser. You'll also find a small blue house icon near the clock (you may need to click the little arrow to see it).

## 2. If the page asks for location access

Windows 11 hides Wi-Fi details from programs unless location access is allowed. LANdlord doesn't use your location, but it needs this switch to read the Wi-Fi signal.

1. Click **Open location settings** on the LANdlord page.
2. Turn on **Location services**.
3. Turn on **Let desktop apps access your location**.

The yellow box on the page disappears within a few seconds.

## 3. While it records

- Whenever a call stutters, a video buffers or a page won't load, press the big red **It's bad right now!** button. You can pick what you were doing first, but you don't have to. Press it every time; more presses make the result more precise.
- You can close the browser tab. LANdlord keeps recording. To get the page back, click the house icon near the clock, or start `landlord.exe` again.
- If the laptop restarts, just start `landlord.exe` again. It continues where it stopped.
- The page shows what LANdlord has found so far. When it says "Most likely cause", you can finish early if you like.

Optional extras on the page:

- **FRITZ!Box password:** if your router is a FRITZ!Box, entering its password lets LANdlord read the line quality from the router. It's the same password you use at `fritz.box`. It's only kept while LANdlord runs.
- **Contracted download speed:** if you know the speed from your internet contract, enter it and the report compares it with the measured speed.

## 4. Get the report

LANdlord stops by itself after 48 hours. You can also click **Finish now and create the report** on the page, or **Finish and create the report** in the house icon's menu.

A folder opens with `report.html` selected. It's in your **Documents** folder under **LANdlord**. Send that file to whoever is helping you, for example by email or messenger. You can open it yourself too; it works in any browser, even without internet.

## Questions

**Does LANdlord slow down my internet?**
Barely. It sends small test packets all the time and runs a short speed test once an hour, which it skips while you're in a call or streaming.

**What does it send to the internet?**
Only test traffic to well-known servers (such as Cloudflare and Google) and to your router. It doesn't upload any results; the report stays on your laptop until you send it.

**How do I stop it without a report?**
Click the house icon near the clock and choose **Pause and quit**. Starting it again later continues the same recording.
