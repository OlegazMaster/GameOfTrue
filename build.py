import re
old=open('/home/claude/roi.html').read()
css=re.search(r'<style>(.*?)</style>',old,re.S).group(1)
for a,b in [('#0e8a6c','#3b4cca'),('#e3f2ec','#e6e9fb'),('#d9f1e4','#dfe4ff'),('#dfe7e0','#e4e7f0'),('rgba(14,138,108,.08)','rgba(59,76,202,.07)'),
            ('#34c29b','#8f9bff'),('#05231a','#0c1240'),('#173229','#1f2550'),('#1c5546','#323c8a'),('rgba(52,194,155,.06)','rgba(143,155,255,.06)'),
            ('#0f1715','#10121a'),('#16201d','#171a24'),('#e6eeeb','#e6e8f0'),('#8a9a95','#8c91a6'),('#24312d','#262a38'),('#1b2724','#1d2130'),('#1c2825','#1f2331'),('#0b1311','#0c0e15'),
            ('#14211d','#161a2c'),('#6b7a75','#6b7088'),('#e4ebe8','#e3e6ef'),('#f2f6f4','#f2f3f8')]:
    css=css.replace(a,b)
html=f'''<!doctype html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover,interactive-widget=resizes-content">
<meta name="theme-color" content="#3b4cca">
<meta name="referrer" content="no-referrer">
<link rel="manifest" href="/manifest.webmanifest">
<link rel="icon" href="/icon.svg" type="image/svg+xml">
<title>ОлеговщинаСМС</title>
<style>{css}{open('src/extra.css').read()}</style>
</head>
<body>
{open('src/body.html').read()}
<script>
{open('src/app.js').read()}
</script>
</body>
</html>
'''
open('web/index.html','w').write(html)

g=open('src/game.html').read().replace('/*GAMEJS*/',open('src/game.js').read())
open('web/game.html','w').write(g)

t=open('src/tanks.html').read().replace('/*TANKSJS*/',open('src/tanks.js').read())
open('web/tanks.html','w').write(t)
