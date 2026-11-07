from pathlib import Path
from PIL import Image,ImageDraw,ImageFont
from fontTools.ttLib import TTFont
font_path='/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf'
font=ImageFont.truetype(font_path,16)
cw=font.getlength('M'); lh=22
out=Path('/tmp/am628nov7-evidence');out.mkdir(exist_ok=True)
frames=[('SSH connection title','ssh-new.txt',None),('Commit rail: reviewed and pending','commit-rail-reviewed.txt',(0,11)),('Commit picker: literal subject preserved','commit-picker.txt',None)]
for stage in ('before','after'):
 panels=[]
 for label,name,crop in frames:
  lines=(Path(f'/tmp/am628-{stage}-nov7-basic')/name).read_text().splitlines()
  if crop: lines=lines[slice(*crop)]
  else:
   while lines and not lines[0].strip():lines.pop(0)
   while lines and not lines[-1].strip():lines.pop()
  panels.append((label,lines))
 h=90+sum(45+len(lines)*lh for _,lines in panels)
 im=Image.new('RGB',(110*round(cw)+48,h),'#151b24');draw=ImageDraw.Draw(im)
 draw.text((24,16),f'{stage.upper()} | Compatible mode | isolated Linux/tmux captures',font=font,fill='#8fd6b0')
 draw.text((24,42),'Rendered text evidence in DejaVu Sans Mono; no terminal font fallback.',font=font,fill='#a6b0bf')
 y=82
 for label,lines in panels:
  draw.text((24,y),label,font=font,fill='#8fd6b0');y+=35
  for line in lines:
   for x,ch in enumerate(line):
    draw.text((24+x*cw,y),ch,font=font,fill='#e5e9f0')
   y+=lh
  y+=10
 im.save(out/f'{stage}.png')
fonts=TTFont(font_path).getBestCmap()
assert all(ord(c) in fonts for c in 'M.+│')
(out/'README.txt').write_text('Before: upstream/main 12cf06d3. After: cb1c990d.\nImages render unmodified plain capture-pane text from real manager runs in Compatible mode. Panels crop blank rows and the lower diff pane; no product text is replaced. These are rendered capture evidence, not native terminal screenshots or a cross-platform font audit.\nReal Codex sign-in sessions were used with disposable HOME/config and explicit isolated tmux sockets.\nKeyboard and mouse picker selection and review marking passed in Basic and Advanced. New/Edit SSH titles and literal Unicode text were checked in both modes.\nDejaVu Sans Mono contains the selected replacement characters M, ., +, and │.\n')
print(out)
