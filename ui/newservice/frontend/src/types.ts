export interface Profile { UplinkMode: "packet" | "tcp-flow"; Transport: "tcp" | "udp"; ObfProfile: "rtpopus2" | "rtpopus3"; Server: string; VkLink: string; Key: string; KeyFile?: string; Mtu: number; Streams: number; StreamsPerCred: number; RouteMode: string }
export interface SavedProfile { id: string; name: string; profile: Profile }
export interface AppSettings {minimizeToTray:boolean;startWithWindows:boolean;autoConnect:boolean}
export interface Snapshot { authRetryAt:number; bypass:BypassSnapshot; bypassActive:boolean; dataDir:string; elapsedSeconds:number; settings:AppSettings; trayReady:boolean; profile: Profile; profiles: SavedProfile[]; activeId: string; state: string; detail: string; underlay: string; ready: number; logs: string[]; rx: number; tx: number }
export interface VersionInfo {appVersion:string;coreVersion:string}
export interface UpdateInfo {currentVersion:string;version:string;available:boolean}
export interface Backend { GetVersionInfo():Promise<VersionInfo>; CheckForUpdates():Promise<UpdateInfo>; InstallUpdate():Promise<void>; SaveBypassSettings(s:BypassSettings):Promise<void>; UpdateRUCIDR():Promise<void>; SaveAppSettings(s:AppSettings):Promise<void>; Exit():Promise<void>; GetSnapshot(): Promise<Snapshot>; SaveProfile(p: Profile): Promise<void>; SaveNamedProfile(p: Profile,name: string,create: boolean): Promise<void>; SelectProfile(id: string): Promise<void>; DeleteProfile(id: string): Promise<void>; Connect(): Promise<void>; Disconnect(): Promise<void>; ExportProfile(p: Profile): Promise<string>; ImportProfile(link: string): Promise<Profile>; CopyConnectionLink(link: string): Promise<void> }

export interface BypassSettings {enabled:boolean;ru:boolean;sites:string[];apps:string[]}
export interface BypassSnapshot {settings:BypassSettings;ruCount:number;ruUpdated:string;error:string}
