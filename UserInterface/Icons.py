import wx
import os
import sys

if getattr(sys, 'frozen', False):
    RES_PATH = os.path.join(sys._MEIPASS, 'res')
else:
    RES_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'res')
    
class ProgramIcon():
    def __init__( self ):
        self.big = wx.IconFromBitmap(wx.Bitmap(os.path.join(RES_PATH, 'icon_48x48.png')))
        self.normal = wx.IconFromBitmap(wx.Bitmap(os.path.join(RES_PATH, 'icon_32x32.png')))
        self.small = wx.IconFromBitmap(wx.Bitmap(os.path.join(RES_PATH, 'icon_16x16.png')))
        
    def GetBig( self ):
        return self.big
        
    def GetNormal( self ):
        return self.normal
        
    def GetSmall( self ):
        return self.small


class TrayIcon():    
    def __init__( self ):
        self.running = wx.IconFromBitmap(wx.Bitmap(os.path.join(RES_PATH, 'tray_running.png')))
        self.paused = wx.IconFromBitmap(wx.Bitmap(os.path.join(RES_PATH, 'tray_paused.png')))
        self.stopped = wx.IconFromBitmap(wx.Bitmap(os.path.join(RES_PATH, 'tray_stopped.png')))
        
    def GetRunning( self ):
        return self.running
        
    def GetPaused( self ):
        return self.paused
        
    def GetStopped( self ):
        return self.stopped