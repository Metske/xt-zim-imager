export namespace main {
	
	export class TestArticle {
	    title: string;
	    content: string;
	
	    static createFrom(source: any = {}) {
	        return new TestArticle(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.content = source["content"];
	    }
	}

}

