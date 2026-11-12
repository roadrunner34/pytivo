export namespace config {
	
	export class Config {
	    server: Record<string, string>;
	    class: Record<string, any>;
	    tivo: Record<string, any>;
	    share: Record<string, any>;
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.server = source["server"];
	        this.class = source["class"];
	        this.tivo = source["tivo"];
	        this.share = source["share"];
	    }
	}
	export class Problem {
	    section: string;
	    name: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new Problem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.section = source["section"];
	        this.name = source["name"];
	        this.message = source["message"];
	    }
	}

}

export namespace core {
	
	export class ImportPreview {
	    config?: config.Config;
	    hasMAK: boolean;
	    tivoMAKs: number;
	    warnings: string[];
	
	    static createFrom(source: any = {}) {
	        return new ImportPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.config = this.convertValues(source["config"], config.Config);
	        this.hasMAK = source["hasMAK"];
	        this.tivoMAKs = source["tivoMAKs"];
	        this.warnings = source["warnings"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Info {
	    version: string;
	    goVersion: string;
	    configPath: string;
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.goVersion = source["goVersion"];
	        this.configPath = source["configPath"];
	    }
	}
	export class NowPlaying {
	    tsn: string;
	    loading: boolean;
	    loaded: number;
	    total: number;
	    error: string;
	    // Go type: time
	    fetched: any;
	    unchanged: boolean;
	    recordings: togo.Recording[];
	
	    static createFrom(source: any = {}) {
	        return new NowPlaying(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tsn = source["tsn"];
	        this.loading = source["loading"];
	        this.loaded = source["loaded"];
	        this.total = source["total"];
	        this.error = source["error"];
	        this.fetched = this.convertValues(source["fetched"], null);
	        this.unchanged = source["unchanged"];
	        this.recordings = this.convertValues(source["recordings"], togo.Recording);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace logx {
	
	export class Entry {
	    seq: number;
	    // Go type: time
	    time: any;
	    level: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new Entry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.seq = source["seq"];
	        this.time = this.convertValues(source["time"], null);
	        this.level = source["level"];
	        this.message = source["message"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace store {
	
	export class Job {
	    id: number;
	    tsn: string;
	    tivoName: string;
	    recording: togo.Recording;
	    ts: boolean;
	    decrypt: boolean;
	    state: string;
	    error: string;
	    path: string;
	    bytes: number;
	    syncLosses: number;
	    attempts: number;
	    // Go type: time
	    created: any;
	    // Go type: time
	    started: any;
	    // Go type: time
	    finished: any;
	    rate: number;
	
	    static createFrom(source: any = {}) {
	        return new Job(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.tsn = source["tsn"];
	        this.tivoName = source["tivoName"];
	        this.recording = this.convertValues(source["recording"], togo.Recording);
	        this.ts = source["ts"];
	        this.decrypt = source["decrypt"];
	        this.state = source["state"];
	        this.error = source["error"];
	        this.path = source["path"];
	        this.bytes = source["bytes"];
	        this.syncLosses = source["syncLosses"];
	        this.attempts = source["attempts"];
	        this.created = this.convertValues(source["created"], null);
	        this.started = this.convertValues(source["started"], null);
	        this.finished = this.convertValues(source["finished"], null);
	        this.rate = source["rate"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace tivo {
	
	export class Box {
	    tsn: string;
	    name: string;
	    address: string;
	    port: number;
	    platform: string;
	    swVersion: string;
	    online: boolean;
	    // Go type: time
	    lastSeen: any;
	
	    static createFrom(source: any = {}) {
	        return new Box(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tsn = source["tsn"];
	        this.name = source["name"];
	        this.address = source["address"];
	        this.port = source["port"];
	        this.platform = source["platform"];
	        this.swVersion = source["swVersion"];
	        this.online = source["online"];
	        this.lastSeen = this.convertValues(source["lastSeen"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace togo {
	
	export class Recording {
	    id: string;
	    title: string;
	    episodeTitle: string;
	    description: string;
	    seriesId: string;
	    programId: string;
	    episodeNumber: string;
	    // Go type: time
	    captured: any;
	    durationMs: number;
	    size: number;
	    channel: string;
	    station: string;
	    hd: boolean;
	    inProgress: boolean;
	    copyProtected: boolean;
	    status: string;
	    tvRating: string;
	    mpaaRating: string;
	    contentUrl: string;
	    detailsUrl: string;
	
	    static createFrom(source: any = {}) {
	        return new Recording(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.episodeTitle = source["episodeTitle"];
	        this.description = source["description"];
	        this.seriesId = source["seriesId"];
	        this.programId = source["programId"];
	        this.episodeNumber = source["episodeNumber"];
	        this.captured = this.convertValues(source["captured"], null);
	        this.durationMs = source["durationMs"];
	        this.size = source["size"];
	        this.channel = source["channel"];
	        this.station = source["station"];
	        this.hd = source["hd"];
	        this.inProgress = source["inProgress"];
	        this.copyProtected = source["copyProtected"];
	        this.status = source["status"];
	        this.tvRating = source["tvRating"];
	        this.mpaaRating = source["mpaaRating"];
	        this.contentUrl = source["contentUrl"];
	        this.detailsUrl = source["detailsUrl"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

