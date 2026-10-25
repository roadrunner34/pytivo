# -*- mode: python -*-

from PyInstaller.utils.hooks import collect_submodules

a = Analysis(['../pyTivoService.py'],
             pathex=['../'],
             binaries=[],
             datas=[('../content/*.*', 'content'),
                ('../templates/*.*', 'templates'),
                ('../plugins/settings/templates/*.*', 'plugins/settings/templates'),
                ('../plugins/settings/content/*.*', 'plugins/settings/content'),
                ('../plugins/settings/help.txt', 'plugins/settings/'),
                ('../plugins/music/templates/*.*', 'plugins/music/templates'),
                ('../plugins/photo/templates/*.*', 'plugins/photo/templates'),
                ('../plugins/togo/templates/*.*', 'plugins/togo/templates'),
                ('../plugins/video/templates/*.*', 'plugins/video/templates'),
                ('../UserInterface/res/*.png', 'UserInterface/res/'),
                ('../videoredo/profiles/*.xml', 'videoredo/profiles/'),],
             hiddenimports=(collect_submodules('zeroconf') + [
                 'plugins.desktop.desktop',
                 'plugins.desktop.asar',
                 'plugins.settings.settings',
                 'plugins.music.music',
                 'plugins.photo.photo',
                 'plugins.togo.togo',
                 'plugins.video.video',
                 'plugins.vrd.vrd']),
             hookspath=[],
             runtime_hooks=[],
             excludes=[],)
pyz = PYZ(a.pure)
exe = EXE(pyz,
          a.scripts,
          a.binaries,
          a.zipfiles,
          a.datas,
          name='pyTivoService',
          debug=False,
          strip=False,
          upx=True,
          console=True,
          icon='../UserInterface/res/icon.ico' )