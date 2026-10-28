# Changelog

## 2.3.3

- Replays are no longer filtered by player name: replays saved while not logged in
  (osu! leaves the name empty) or with a different name in the osu! config are used too.
  The "Player name to analyse" setting is gone.
- The OpenTabletDriver window can stay open: when it saves an old area (Save with the old
  numbers), the diagnosis area is put back in the file too, instead of pausing. The notice
  about the window is clearer, with a "Show the current area in OpenTabletDriver" button.
- Once the diagnosis is sure enough, the width tries vary in size, so the widths in
  between are played too instead of always the same two areas.

## 2.3.2

- Works with any area rotation in OpenTabletDriver (90°, 180°, 270° or any other angle):
  the first area is the largest that fits the tablet turned that way, every area stays
  inside the tablet, the side tries and the turned-aim correction follow the area's own
  width and height, and directions in the interface are as seen on the screen.

## 2.3.1

- Only the replays saved since you first started Area Coach are used: your old replays
  are no longer imported (with a big replay folder that import could take very long).
  Plays from before the first start are removed from the analysis.

## 2.3.0 — first public release

- Interface, messages and log in English.
- In-game diagnosis, map by map: the area for the next map is set in OpenTabletDriver
  when osu! saves the replay; every map tries a slightly different width and, every two
  maps, a slightly different place, to find where you are most precise and steadiest.
- Guided Radial Follow filter: the program tells you the values for the next map, you set
  them in OpenTabletDriver, and after every replay it gives you new ones. Choice between
  the lowest latency and more smoothing.
- Area and filter "perfection" percentages.
- Replay import: failed and multiplayer plays are picked up when saved with F2; the Plays
  page lists the replays found but not used, with the reason, and the folders it watches.
- Grip profiles, area history, replay analysis pages (Area, Progress, Plays, Filter).
