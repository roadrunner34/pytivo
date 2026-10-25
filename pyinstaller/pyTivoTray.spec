# -*- mode: python -*-

from PyInstaller.utils.hooks import collect_submodules

a = Analysis(['../UserInterface/pyTivoTray.py'],
             pathex=['../UserInterface/', '../'],
             binaries=[],
             datas=[('../UserInterface/res/*.png', 'res/')],
             hiddenimports=[],
             hookspath=[],
             runtime_hooks=[],
             excludes=[],)

pyz = PYZ(a.pure)
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