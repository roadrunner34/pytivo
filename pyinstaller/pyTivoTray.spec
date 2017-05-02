# -*- mode: python -*-

block_cipher = None


a = Analysis(['../UserInterface/pyTivoTray.py'],
             pathex=['../UserInterface/'],
             binaries=[],
             datas=[('../UserInterface/res/*.png', 'res/')],
             hiddenimports=[],
             hookspath=[],
             runtime_hooks=[],
             excludes=[],
             win_no_prefer_redirects=False,
             win_private_assemblies=False,
             cipher=block_cipher)
             
pyz = PYZ(a.pure, a.zipped_data,
             cipher=block_cipher)
exe = EXE(pyz,
          a.scripts,
          a.binaries,
          a.zipfiles,
          a.datas,
          name='pyTivoTray',
          debug=False,
          strip=False,
          upx=True,
          console=False,
		  icon='../UserInterface/res/icon.ico' )

app = BUNDLE(exe,
		  name='pyTivoTray.app',
		  icon='../UserInterface/res/icon.icns',
		  bundle_identifier='com.pytivo.pytivotray',
          info_plist={'LSUIElement': '1'})